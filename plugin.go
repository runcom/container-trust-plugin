package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/net/context"

	"github.com/containers/image/docker"
	"github.com/containers/image/manifest"
	"github.com/containers/image/signature"
	"github.com/docker/distribution/digest"
	distreference "github.com/docker/distribution/reference"
	dockerapi "github.com/docker/docker/api"
	"github.com/docker/docker/reference"
	dockerclient "github.com/docker/engine-api/client"
	engineapitypes "github.com/docker/engine-api/types"
	"github.com/docker/go-connections/sockets"
	"github.com/docker/go-plugins-helpers/authorization"
)

type conf struct {
	Enabled  bool
	AutoPull bool
}

const (
	pluginConfPath = "/etc/docker/trust-plugin.yaml"
)

func newPlugin(dockerHost, certPath string, tlsVerify bool) (*trustPlugin, error) {
	confFile, err := os.Open(pluginConfPath)
	if err != nil {
		return nil, err
	}
	defer confFile.Close()

	var config conf
	if err := json.NewDecoder(confFile).Decode(&config); err != nil {
		return nil, err
	}
	c := &http.Client{}
	if certPath != "" {
		tlsc := &tls.Config{}

		cert, err := tls.LoadX509KeyPair(filepath.Join(certPath, "cert.pem"), filepath.Join(certPath, "key.pem"))
		if err != nil {
			return nil, fmt.Errorf("Error loading x509 key pair: %s", err)
		}

		tlsc.Certificates = append(tlsc.Certificates, cert)
		tlsc.InsecureSkipVerify = !tlsVerify
		transport := &http.Transport{
			TLSClientConfig: tlsc,
		}
		c.Transport = transport
	} else {
		proto, addr, _, err := dockerclient.ParseHost(dockerHost)
		if err != nil {
			return nil, err
		}
		tr := new(http.Transport)
		sockets.ConfigureTransport(tr, proto, addr)
		c.Transport = tr
	}

	client, err := dockerclient.NewClient(dockerHost, dockerapi.DefaultVersion, c, nil)
	if err != nil {
		return nil, err
	}
	return &trustPlugin{client: client, config: config}, nil
}

var (
	pullRegExp = regexp.MustCompile(`/images/create(\?fromImage=([^&]*)(&tag=(.*)?)?)?$`)
)

type trustPlugin struct {
	config conf
	client *dockerclient.Client
}

func (p *trustPlugin) AuthZReq(req authorization.Request) authorization.Response {
	if req.RequestMethod == "POST" && pullRegExp.MatchString(req.RequestURI) {
		decoded_url, err := url.QueryUnescape(req.RequestURI)
		if err != nil {
			return authorization.Response{Err: err.Error()}
		}
		res := pullRegExp.FindStringSubmatch(decoded_url)
		if len(res) < 5 {
			return authorization.Response{Err: "unable to find repository name and reference"}
		}
		ref, err := reference.ParseNamed(res[2])
		if err != nil {
			return authorization.Response{Err: err.Error()}
		}

		var isByDigest bool
		if res[4] != "" {
			// The "tag" could actually be a digest.
			var dgst digest.Digest
			dgst, err = digest.ParseDigest(res[4])
			if err == nil {
				ref, err = reference.WithDigest(ref, dgst)
				isByDigest = true
			} else {
				ref, err = reference.WithTag(ref, res[4])
			}
			if err != nil {
				return authorization.Response{Err: err.Error()}
			}
		} else {
			return authorization.Response{Err: "unable to verify all tags for the given image"}
		}
		if reference.IsNameOnly(ref) {
			ref = reference.WithDefaultTag(ref)
		}

		registries, err := p.getAdditionalDockerRegistries()
		if err != nil {
			return authorization.Response{Err: err.Error()}
		}

		// Pull with an unqualified image and projectatomic/docker
		//
		// this is the case where the plugin is talking to a projectatomic/docker
		// and we can't have the signature check because we can only control the
		// first registry in "registries" and we can't say anything about the others
		// which will be tried inside the daemon.
		//
		// if this chekc is false we assume the first registry is docker.io
		// and the signature check  can be done below.
		if !isReferenceFullyQualified(ref) && len(registries) > 1 {
			return authorization.Response{Err: "can't check signatures, please pull with a fully qualified image name"}
		}

		var defaultRegistry string
		if len(registries) != 0 {
			defaultRegistry = registries[0]
		}

		// If we're talking to a projectatomic/docker and one has --block-registry=public
		// and --add-registry=redhat.io, we'll qualify the reference with that
		// registry configured as the first.
		//
		// docker pull rhel/rhel7 # --add-registry=redhat.io --block-registry=public
		// ref == redhat.io/rhel/rhel7
		if defaultRegistry != "" && defaultRegistry != "docker.io" {
			ref, err = qualifyUnqualifiedReference(ref, defaultRegistry)
			if err != nil {
				return authorization.Response{Err: err.Error()}
			}
		}

		// otherwise, ref is fine to be used now in case we're talking to
		// a docker/docker engine.

		imgRef, err := docker.NewReference(ref)
		if err != nil {
			return authorization.Response{Err: err.Error()}
		}
		img, err := imgRef.NewImage(nil)
		if err != nil {
			return authorization.Response{Err: err.Error()}
		}
		defaultPolicy, err := signature.DefaultPolicy(nil)
		if err != nil {
			return authorization.Response{Err: err.Error()}
		}
		pc, err := signature.NewPolicyContext(defaultPolicy)
		if err != nil {
			return authorization.Response{Err: err.Error()}
		}
		allowed, err := pc.IsRunningImageAllowed(img)
		if err != nil {
			return authorization.Response{Err: err.Error()}
		}
		d, _, err := img.Manifest()
		if err != nil {
			return authorization.Response{Err: err.Error()}
		}
		digest, err := manifest.Digest(d)
		if err != nil {
			return authorization.Response{Err: err.Error()}
		}
		if allowed {
			if isByDigest {
				if res[4] == digest {
					goto allow
				} else {
					return authorization.Response{Err: fmt.Sprintf("digests mismatch, provided %s, computed %s", res[4], digest)}
				}
			} else {
				if p.config.AutoPull {
					newRef, err := reference.ParseNamed(res[2] + "@" + digest)
					if err != nil {
						return authorization.Response{Err: err.Error()}
					}
					// TODO(runcom): fix the last arg to provide authconfig and requestprivilegdfunc in the options
					r, err := p.client.ImagePull(context.Background(), newRef.String(), engineapitypes.ImagePullOptions{})
					if err != nil {
						return authorization.Response{Err: err.Error()}
					}
					// Should wait for pull to finish streaming
					_, err = ioutil.ReadAll(r)
					if err != nil {
						return authorization.Response{Err: err.Error()}
					}
					r.Close()

					if err := p.client.ImageTag(context.Background(), newRef.String(), res[2]+":"+res[4]); err != nil {
						return authorization.Response{Err: err.Error()}
					}
					goto allow
				} else {
					return authorization.Response{Err: fmt.Sprintf("image is allowed but can't pull by tag. Pull the image with 'docker pull %s@%s' and tag it with 'docker tag %s@%s %s:%s'", res[2], digest, res[2], digest, res[2], res[4])}
				}
			}
		}
		goto noallow
	}
allow:
	return authorization.Response{Allow: true}

noallow:
	return authorization.Response{Msg: "image isn't allowed"}
}

func (p *trustPlugin) AuthZRes(req authorization.Request) authorization.Response {
	return authorization.Response{Allow: true}
}

func (p *trustPlugin) getAdditionalDockerRegistries() ([]string, error) {
	ctx := context.Background()
	// XXX: official engine-api client doesn't have Registries in Info() response
	// hacked into vendor/github.com/docker/engine-api/types/types.go
	i, err := p.client.Info(ctx)
	if err != nil {
		return nil, err
	}
	regs := []string{}
	for _, r := range i.Registries {
		regs = append(regs, r.Name)
	}
	return regs, nil
}

// isReferenceFullyQualified determines whether the given reposName has prepended
// name of index.
func isReferenceFullyQualified(reposName reference.Named) bool {
	indexName, _, _ := splitReposName(reposName)
	return indexName != ""
}

// splitReposName breaks a reposName into an index name and remote name
func splitReposName(reposName reference.Named) (indexName string, remoteName reference.Named, err error) {
	var remoteNameStr string
	indexName, remoteNameStr = distreference.SplitHostname(reposName)
	if !isValidHostname(indexName) {
		// This is a Docker Index repos (ex: samalba/hipache or ubuntu)
		// 'docker.io'
		indexName = ""
		remoteName = reposName
	} else {
		remoteName, err = reference.WithName(remoteNameStr)
	}
	return
}

func isValidHostname(hostname string) bool {
	return hostname != "" && !strings.Contains(hostname, "/") &&
		(strings.Contains(hostname, ".") ||
			strings.Contains(hostname, ":") || hostname == "localhost")
}

func qualifyUnqualifiedReference(ref reference.Named, indexName string) (reference.Named, error) {
	if !isValidHostname(indexName) {
		return nil, fmt.Errorf("Invalid hostname %q", indexName)
	}
	orig, remoteName, err := splitReposName(ref)
	if err != nil {
		return nil, err
	}
	if orig == "" {
		return substituteReferenceName(ref, indexName+"/"+remoteName.Name())
	}
	return ref, nil
}

// substituteReferenceName creates a new image reference from given ref with
// its *name* part substituted for reposName.
func substituteReferenceName(ref reference.Named, reposName string) (newRef reference.Named, err error) {
	reposNameRef, err := reference.WithName(reposName)
	if err != nil {
		return nil, err
	}
	if tagged, isTagged := ref.(distreference.Tagged); isTagged {
		newRef, err = reference.WithTag(reposNameRef, tagged.Tag())
		if err != nil {
			return nil, err
		}
	} else if digested, isDigested := ref.(distreference.Digested); isDigested {
		newRef, err = reference.WithDigest(reposNameRef, digested.Digest())
		if err != nil {
			return nil, err
		}
	} else {
		newRef = reposNameRef
	}
	return
}
