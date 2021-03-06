package layout

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/containers/image/manifest"
	"github.com/containers/image/types"
	imgspec "github.com/opencontainers/image-spec/specs-go"
	imgspecv1 "github.com/opencontainers/image-spec/specs-go/v1"
)

type ociImageDestination struct {
	ref ociReference
}

// newImageDestination returns an ImageDestination for writing to an existing directory.
func newImageDestination(ref ociReference) types.ImageDestination {
	return &ociImageDestination{ref: ref}
}

// Reference returns the reference used to set up this destination.  Note that this should directly correspond to user's intent,
// e.g. it should use the public hostname instead of the result of resolving CNAMEs or following redirects.
func (d *ociImageDestination) Reference() types.ImageReference {
	return d.ref
}

// Close removes resources associated with an initialized ImageDestination, if any.
func (d *ociImageDestination) Close() {
}

func (d *ociImageDestination) SupportedManifestMIMETypes() []string {
	return []string{
		imgspecv1.MediaTypeImageManifest,
		manifest.DockerV2Schema2MediaType,
	}
}

// SupportsSignatures returns an error (to be displayed to the user) if the destination certainly can't store signatures.
// Note: It is still possible for PutSignatures to fail if SupportsSignatures returns nil.
func (d *ociImageDestination) SupportsSignatures() error {
	return fmt.Errorf("Pushing signatures for OCI images is not supported")
}

// ShouldCompressLayers returns true iff it is desirable to compress layer blobs written to this destination.
func (d *ociImageDestination) ShouldCompressLayers() bool {
	return false
}

// PutBlob writes contents of stream and returns data representing the result (with all data filled in).
// inputInfo.Digest can be optionally provided if known; it is not mandatory for the implementation to verify it.
// inputInfo.Size is the expected length of stream, if known.
// WARNING: The contents of stream are being verified on the fly.  Until stream.Read() returns io.EOF, the contents of the data SHOULD NOT be available
// to any other readers for download using the supplied digest.
// If stream.Read() at any time, ESPECIALLY at end of input, returns an error, PutBlob MUST 1) fail, and 2) delete any data stored so far.
func (d *ociImageDestination) PutBlob(stream io.Reader, inputInfo types.BlobInfo) (types.BlobInfo, error) {
	if err := ensureDirectoryExists(d.ref.dir); err != nil {
		return types.BlobInfo{}, err
	}
	blobFile, err := ioutil.TempFile(d.ref.dir, "oci-put-blob")
	if err != nil {
		return types.BlobInfo{}, err
	}
	succeeded := false
	defer func() {
		blobFile.Close()
		if !succeeded {
			os.Remove(blobFile.Name())
		}
	}()

	h := sha256.New()
	tee := io.TeeReader(stream, h)

	size, err := io.Copy(blobFile, tee)
	if err != nil {
		return types.BlobInfo{}, err
	}
	computedDigest := "sha256:" + hex.EncodeToString(h.Sum(nil))
	if inputInfo.Size != -1 && size != inputInfo.Size {
		return types.BlobInfo{}, fmt.Errorf("Size mismatch when copying %s, expected %d, got %d", computedDigest, inputInfo.Size, size)
	}
	if err := blobFile.Sync(); err != nil {
		return types.BlobInfo{}, err
	}
	if err := blobFile.Chmod(0644); err != nil {
		return types.BlobInfo{}, err
	}

	blobPath, err := d.ref.blobPath(computedDigest)
	if err != nil {
		return types.BlobInfo{}, err
	}
	if err := ensureParentDirectoryExists(blobPath); err != nil {
		return types.BlobInfo{}, err
	}
	if err := os.Rename(blobFile.Name(), blobPath); err != nil {
		return types.BlobInfo{}, err
	}
	succeeded = true
	return types.BlobInfo{Digest: computedDigest, Size: size}, nil
}

func createManifest(m []byte) ([]byte, string, error) {
	om := imgspecv1.Manifest{}
	mt := manifest.GuessMIMEType(m)
	switch mt {
	case manifest.DockerV2Schema1MediaType, manifest.DockerV2Schema1SignedMediaType:
		// There a simple reason about not yet implementing this.
		// OCI image-spec assure about backward compatibility with docker v2s2 but not v2s1
		// generating a v2s2 is a migration docker does when upgrading to 1.10.3
		// and I don't think we should bother about this now (I don't want to have migration code here in skopeo)
		return nil, "", errors.New("can't create an OCI manifest from Docker V2 schema 1 manifest")
	case manifest.DockerV2Schema2MediaType:
		if err := json.Unmarshal(m, &om); err != nil {
			return nil, "", err
		}
		om.MediaType = imgspecv1.MediaTypeImageManifest
		for i := range om.Layers {
			om.Layers[i].MediaType = imgspecv1.MediaTypeImageLayer
		}
		om.Config.MediaType = imgspecv1.MediaTypeImageConfig
		b, err := json.Marshal(om)
		if err != nil {
			return nil, "", err
		}
		return b, om.MediaType, nil
	case manifest.DockerV2ListMediaType:
		return nil, "", errors.New("can't create an OCI manifest from Docker V2 schema 2 manifest list")
	case imgspecv1.MediaTypeImageManifestList:
		return nil, "", errors.New("can't create an OCI manifest from OCI manifest list")
	case imgspecv1.MediaTypeImageManifest:
		return m, mt, nil
	}
	return nil, "", fmt.Errorf("unrecognized manifest media type %q", mt)
}

func (d *ociImageDestination) PutManifest(m []byte) error {
	// TODO(mitr, runcom): this breaks signatures entirely since at this point we're creating a new manifest
	// and signatures don't apply anymore. Will fix.
	ociMan, mt, err := createManifest(m)
	if err != nil {
		return err
	}
	digest, err := manifest.Digest(ociMan)
	if err != nil {
		return err
	}
	desc := imgspec.Descriptor{}
	desc.Digest = digest
	// TODO(runcom): beaware and add support for OCI manifest list
	desc.MediaType = mt
	desc.Size = int64(len(ociMan))
	data, err := json.Marshal(desc)
	if err != nil {
		return err
	}

	blobPath, err := d.ref.blobPath(digest)
	if err != nil {
		return err
	}
	if err := ioutil.WriteFile(blobPath, ociMan, 0644); err != nil {
		return err
	}
	// TODO(runcom): ugly here?
	if err := ioutil.WriteFile(d.ref.ociLayoutPath(), []byte(`{"imageLayoutVersion": "1.0.0"}`), 0644); err != nil {
		return err
	}
	descriptorPath := d.ref.descriptorPath(d.ref.tag)
	if err := ensureParentDirectoryExists(descriptorPath); err != nil {
		return err
	}
	return ioutil.WriteFile(descriptorPath, data, 0644)
}

func ensureDirectoryExists(path string) error {
	if _, err := os.Stat(path); err != nil && os.IsNotExist(err) {
		if err := os.MkdirAll(path, 0755); err != nil {
			return err
		}
	}
	return nil
}

// ensureParentDirectoryExists ensures the parent of the supplied path exists.
func ensureParentDirectoryExists(path string) error {
	return ensureDirectoryExists(filepath.Dir(path))
}

func (d *ociImageDestination) PutSignatures(signatures [][]byte) error {
	if len(signatures) != 0 {
		return fmt.Errorf("Pushing signatures for OCI images is not supported")
	}
	return nil
}

// Commit marks the process of storing the image as successful and asks for the image to be persisted.
// WARNING: This does not have any transactional semantics:
// - Uploaded data MAY be visible to others before Commit() is called
// - Uploaded data MAY be removed or MAY remain around if Close() is called without Commit() (i.e. rollback is allowed but not guaranteed)
func (d *ociImageDestination) Commit() error {
	return nil
}
