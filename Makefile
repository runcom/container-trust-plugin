.PHONY: all binary man install clean
export GOPATH:=$(CURDIR)/Godeps/_workspace:$(GOPATH)

LIBDIR=${DESTDIR}/lib/systemd/system
BINDIR=${DESTDIR}/usr/libexec/docker/
CONFDIR=${DESTDIR}/etc/docker
CONTAINERSDIR=${DESTDIR}/etc/containers
PREFIX ?= ${DESTDIR}/usr
MANINSTALLDIR=${PREFIX}/share/man

all: man binary

## this uses https://github.com/Masterminds/glide and https://github.com/sgotti/glide-vc
update-deps:
	glide update --strip-vcs --strip-vendor --update-vendored --delete
	glide-vc --only-code --no-tests
	# see http://sed.sourceforge.net/sed1line.txt
	find vendor -type f -exec sed -i -e :a -e '/^\n*$$/{$$d;N;ba' -e '}' "{}" \;
	git apply engine-api.patch

binary:
	go build  -o trust-plugin .

man:
	go-md2man -in man/trust-plugin.8.md -out trust-plugin.8

install:
	install -d -m 0755 ${CONFDIR}
	install -m 644 trust-plugin.yaml ${CONFDIR}/trust-plugin.yaml
	install -d -m 0755 ${CONTAINERSDIR}
	install -m 644 default-policy.json ${CONTAINERSDIR}/policy.json
	install -d -m 0755 ${LIBDIR}
	install -m 644 systemd/trust-plugin.service ${LIBDIR}
	install -d -m 0755 ${LIBDIR}
	install -m 644 systemd/trust-plugin.socket ${LIBDIR}
	install -d -m 0755 ${BINDIR}
	install -m 755 trust-plugin ${BINDIR}
	install -m 644 trust-plugin.8 ${MANINSTALLDIR}/man8/

clean:
	rm -f trust-plugin
	rm -f trust-plugin.8
