# Contributing

Issues and pull requests are welcome.

## Development setup

You need Go 1.23+ (the toolchain in `go.mod` is fetched automatically), git and tmux.

```bash
git clone https://github.com/AlexanderWeismannn/adroit && cd adroit
go build -o adroit . && ./adroit doctor
go test ./...
```

Run your build against a scratch repository rather than one you care about:
sessions create real worktrees and branches. Logs go to `/tmp/adroit.log`.

If you run the tests from inside tmux (or inside Adroit itself), clear `TMUX`
so the tmux tests start their own server: `env -u TMUX go test ./...`.

## Code standards

- `gofmt -w .` before committing; CI fails on unformatted files.
- CI runs `golangci-lint` on changed lines.
- Include a test that fails without your change when you fix a bug.
- Comments explain *why*: the failure a line prevents, not what it does.

## Releasing

Releases are cut by pushing a tag. The release workflow runs the tests, then
GoReleaser builds linux/macOS × amd64/arm64 archives with the version stamped in
and publishes them with `checksums.txt`, which is what `install.sh` downloads.

```bash
git tag v1.2.0 && git push origin v1.2.0
```

A tag with a suffix (`v1.2.0-rc.1`) is published as a pre-release, which the
installer skips unless asked for with `--version`.
