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

## The README recording

`assets/demo.gif` is recorded from the real TUI, not drawn. `demo/record.py`
builds a throwaway home, repository and tmux server, runs `adroit` in a
pseudo-terminal with `demo/agent` standing in for the coding agent, and types a
fixed script of keys. [agg](https://github.com/asciinema/agg) renders the result:

```bash
go build -o /tmp/adroit . && python3 demo/record.py /tmp/adroit /tmp/demo.cast
agg --theme nord --font-size 17 --idle-time-limit 2 /tmp/demo.cast assets/demo.gif
# the site plays an MP4 instead: sharper, and it can pause
ffmpeg -i assets/demo.gif -movflags +faststart -pix_fmt yuv420p \
  -vf "scale=trunc(iw/2)*2:trunc(ih/2)*2" -c:v libx264 -crf 22 site/demo.mp4
```

Re-record it when the interface changes.

## Releasing

Releases are cut by pushing a tag. The release workflow runs the tests, then
GoReleaser builds linux/macOS × amd64/arm64 archives with the version stamped in
and publishes them with `checksums.txt`, which is what `install.sh` downloads.

```bash
git tag v1.2.0 && git push origin v1.2.0
```

A tag with a suffix (`v1.2.0-rc.1`) is published as a pre-release, which the
installer skips unless asked for with `--version`.
