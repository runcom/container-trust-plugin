Trust plugin
=
_In order to use this plugin you must be running at least Docker 1.10 which
has support for authorization plugins._

TODO(runcom): add description

Building
-
```sh
$ export GOPATH=~ # optional if you already have this
$ mkdir -p ~/src/github.com/projectatomic # optional, from now on I'm assuming GOPATH=~
$ cd ~/src/github.com/projectatomic && git clone https://github.com/projectatomic/trust-plugin
$ cd trust-plugin
$ make
```
Installing
-
```sh
$ sudo make install
$ systemctl enable trust-plugin
```
Running
-
Specify `--authorization-plugin=trust-plugin` in the `docker daemon` command line
flags (either in the systemd unit file or in `/etc/sysconfig/docker` in `$OPTIONS`
or when manually starting the daemon).
The plugin must be started before `docker` (done automatically via systemd unit file).
If you're not using the systemd unit file:
```sh
$ trust-plugin &
```
Just restart `docker` and you're good to go!
Systemd socket activation
-
The plugin can be socket activated by systemd. You just have to basically use the file provided
under `systemd/` (or installing via `make install`). This ensures the plugin gets activated
if it goes down for any reason.
How to test
-

TODO(runcom): add how to test

License
-
GPLv2
