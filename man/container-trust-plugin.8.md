% CONTAINER-TRUST-PLUGIN(8)
% Antonio Murdaca
% MARCH 2016
# NAME
container-trust-plugin - Block pulls from untrusted registries

# SYNOPSIS
**container-trust-plugin**
[**--cert-path**=[=*""*]]
[**--host**=[=*unix:///var/run/docker.sock*]]
[**--tls-verify**=[=*false*]]

# DESCRIPTION

TODO(runcom): fix description

Red Hat subscription agreement prevents users from posting of Red Hat based content to public registries.
This plugin looks at the base image of any container image that is being pushed to the default registry,
`docker.io`, and blocks the push, if the content uses a RHEL base image. Users can push RHEL content to
their own private registries.  You can modify the docker service to support private registies by using
the --add-registry docker daemon flag.  You can add this to the docker daemon command or by adding it to
the OPTIONS configuation in /etc/sysconfig/docker


# OPTIONS

**--cert-path**=""
  Certificates path to connect to Docker (cert.pem, key.pem)
**--host**="unix:///var/run/docker.sock"
  Specifies the host where to contact the docker daemon.
**--tls-verify**="false"
  Whether to verify certificates or not

# AUTHORS
Antonio Murdaca <runcom@redhat.com>
