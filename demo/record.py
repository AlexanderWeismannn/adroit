#!/usr/bin/env python3
"""Record the README demo from the real Adroit TUI.

Builds a throwaway home directory, git repository and tmux server, runs the
adroit binary in a pseudo-terminal with demo/agent standing in for the coding
agent, types a fixed script of keys into it, and writes what the terminal
showed as an asciicast. Turn that into a GIF with agg:

    go build -o /tmp/adroit . && python3 demo/record.py /tmp/adroit demo.cast
    agg --theme nord --font-size 17 --idle-time-limit 2 demo.cast assets/demo.gif

Nothing outside the temporary directory is touched: HOME, TMUX_TMPDIR and PATH
all point into it, so your own sessions and config are never seen.
"""
import codecs, fcntl, json, os, pty, re, select, shutil, signal, struct, subprocess, sys, tempfile, termios, threading, time

COLS, ROWS = 110, 30
HERE = os.path.dirname(os.path.abspath(__file__))

REFRESH = """package auth

import "time"

// Client sends requests with a bearer token.
type Client struct {
	token Token
}

func (c *Client) Do(req *Request) (*Response, error) {
	resp, err := c.send(req)
	if isExpired(c.token) {
		c.refresh()
	}
	return resp, err
}

func isExpired(t Token) bool { return time.Now().After(t.Expiry) }
"""
RETRY = """package billing

// Charge tries the payment once.
func Charge(p Payment) error {
	return gateway.Charge(p)
}
"""


def sandbox(root, adroit):
    home = os.path.join(root, "home")
    repo = os.path.join(home, "code", "api")
    bindir = os.path.join(root, "bin")
    for d in (os.path.join(repo, "auth"), os.path.join(repo, "billing"), bindir, os.path.join(home, ".adroit")):
        os.makedirs(d, exist_ok=True)
    files = {"go.mod": "module example.com/api\n\ngo 1.23\n", "auth/refresh.go": REFRESH,
             "billing/retry.go": RETRY, "README.md": "# api\n\nThe example service.\n"}
    for name, body in files.items():
        with open(os.path.join(repo, name), "w") as f:
            f.write(body)
    git = ["git", "-C", repo, "-c", "user.name=jane", "-c", "user.email=jane@example.com"]
    subprocess.run(git[:3] + ["init", "-q", "-b", "main"], check=True)
    subprocess.run(git + ["add", "-A"], check=True)
    subprocess.run(git + ["commit", "-qm", "Initial commit"], check=True)
    subprocess.run(["git", "-C", repo, "config", "user.name", "jane"], check=True)
    subprocess.run(["git", "-C", repo, "config", "user.email", "jane@example.com"], check=True)

    # The dev stack `d` starts: two tiny servers, so the Run tab has two lamps to
    # turn green. TCPServer rather than http.server.HTTPServer, whose getfqdn()
    # between bind and listen can stall for seconds.
    serve = ("python3 -c 'import socketserver,http.server,sys; socketserver.TCPServer.allow_reuse_address=True; "
             "socketserver.TCPServer((\"127.0.0.1\", int(sys.argv[1])), http.server.SimpleHTTPRequestHandler)"
             ".serve_forever()' ")
    dev = {
        "command": f"echo 'api   listening on :38081'; {serve}38081 2>/dev/null & sleep 1.2; "
                   f"echo 'web   ready on http://localhost:38080'; {serve}38080 2>/dev/null; wait",
        "checks": [
            {"name": "api", "type": "http", "target": "http://127.0.0.1:38081/", "own_cwd": True},
            {"name": "web", "type": "http", "target": "http://127.0.0.1:38080/", "own_cwd": True},
        ],
        "ready_timeout_seconds": 30,
    }
    with open(os.path.join(home, ".adroit", "config.json"), "w") as f:
        json.dump({"default_program": "claude", "branch_prefix": "jane/", "theme": "nord",
                   "github_ci_status": False, "upstream_status": False, "sync_base_branch": False,
                   "bell": False, "daemon_poll_interval": 1000,
                   "repos": {repo: {"dev": dev}}}, f, indent=2)
    # Each help card is shown once per install; a demo of a returning user
    # should not stop on them. The field is a bitmask, so this marks them all.
    with open(os.path.join(home, ".adroit", "state.json"), "w") as f:
        json.dump({"help_screens_seen": 0xFFFF, "instances": []}, f)

    # Attached sessions are plain tmux; without this they draw tmux's own status
    # bar, hostname and clock included.
    with open(os.path.join(home, ".tmux.conf"), "w") as f:
        f.write("set -g status off\n")

    os.symlink(os.path.join(HERE, "agent"), os.path.join(bindir, "claude"))
    os.symlink(os.path.abspath(adroit), os.path.join(bindir, "adroit"))
    env = {
        "HOME": home, "PATH": bindir + ":/usr/local/bin:/usr/bin:/bin", "SHELL": "/bin/bash",
        "TERM": "xterm-256color", "COLORTERM": "truecolor", "LANG": "C.UTF-8",
        "TMUX_TMPDIR": root, "PS1": "jane@box:\\w$ ",
    }
    return repo, env


# Queries a terminal is expected to answer. A pty answers nothing, so without
# these the program waits out its timeouts or guesses a light background.
REPLIES = [
    (re.compile(rb"\x1b\]11;\?(\x07|\x1b\\)"), b"\x1b]11;rgb:2e2e/3434/4040\x1b\\"),
    (re.compile(rb"\x1b\]10;\?(\x07|\x1b\\)"), b"\x1b]10;rgb:d8d8/dede/e9e9\x1b\\"),
    (re.compile(rb"\x1b\[6n"), b"\x1b[1;1R"),
    (re.compile(rb"\x1b\[c"), b"\x1b[?62;22c"),
]


def record(adroit, cast_path):
    root = tempfile.mkdtemp(prefix="adr", dir="/tmp")
    try:
        repo, env = sandbox(root, adroit)
        pid, fd = pty.fork()
        if pid == 0:
            os.chdir(repo)
            os.execve(os.path.join(env["PATH"].split(":")[0], "adroit"), ["adroit"], env)
        fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", ROWS, COLS, 0, 0))

        events, start, done = [], time.time(), threading.Event()
        # One decoder for the whole stream: a read can end halfway through a
        # character, and decoding each read alone turns that character into two
        # replacement cells, which wraps the line and scrolls the screen.
        decoder = codecs.getincrementaldecoder("utf-8")("replace")

        def reader():
            while not done.is_set():
                r, _, _ = select.select([fd], [], [], 0.1)
                if not r:
                    continue
                try:
                    data = os.read(fd, 65536)
                except OSError:
                    break
                if not data:
                    break
                for pattern, reply in REPLIES:
                    if pattern.search(data):
                        os.write(fd, reply)
                text = decoder.decode(data)
                if text:
                    events.append([round(time.time() - start, 4), "o", text])

        t = threading.Thread(target=reader, daemon=True)
        t.start()

        def keys(s, gap=0.0):
            os.write(fd, s.encode())
            time.sleep(gap)

        def typed(s, speed=0.055, after=0.4):
            for ch in s:
                keys(ch, speed)
            time.sleep(after)

        wait = time.sleep
        ENTER, ESC, TAB, CTRL_S, CTRL_Q, UP, DOWN = "\r", "\x1b", "\t", "\x13", "\x11", "\x1b[A", "\x1b[B"

        wait(1.8)
        # 1. three sessions, each started with its prompt, all working at once
        for name, prompt in (("auth-refresh", "refresh the token before the request is sent"),
                             ("billing-retry", "retry failed charges with backoff"),
                             ("readme-docs", "add a quick start to the README")):
            keys("N", 0.5); typed(name, 0.045, 0.2); keys(ENTER, 0.8)
            typed(prompt, 0.03, 0.3); keys(CTRL_S, 1.6)
        # 2. look in on them while they work
        keys("k", 1.8); keys("k", 2.2); keys("j", 2.4)
        wait(3.0)
        # 3. everything has finished; read what one of them changed
        keys("k", 1.2)
        keys(TAB, 4.5)
        # 4. run the dev stack in that worktree; the lamps go green
        keys("d", 6.0)
        keys(TAB, 0.3)
        # 5. the theme picker recolours everything as it moves
        keys("t", 0.9)
        for _ in range(3):
            keys(UP, 1.0)
        keys(ESC, 1.2)
        # 6. attach, say something, detach
        keys(ENTER, 2.4); typed("thanks, ship it", 0.05, 0.3); keys(ENTER, 2.4); keys(CTRL_Q, 2.2)

        done.set()
        t.join(1)
        os.kill(pid, signal.SIGTERM)
        with open(cast_path, "w") as f:
            f.write(json.dumps({"version": 2, "width": COLS, "height": ROWS,
                                "env": {"TERM": "xterm-256color", "SHELL": "/bin/bash"}}) + "\n")
            for e in events:
                f.write(json.dumps(e) + "\n")
        print(f"wrote {len(events)} events, {events[-1][0]:.1f}s, to {cast_path}")
    finally:
        # Without TMUX in the environment, kill-server can only reach the sandbox's own server.
        clean = {k: v for k, v in os.environ.items() if k not in ("TMUX", "TMUX_PANE")}
        subprocess.run(["tmux", "kill-server"], env={**clean, "TMUX_TMPDIR": root}, stderr=subprocess.DEVNULL)
        # The stack's servers are their own process group; make sure none outlives the run.
        subprocess.run(["pkill", "-f", "socketserver.TCPServer.allow_reuse_address"], stderr=subprocess.DEVNULL)
        shutil.rmtree(root, ignore_errors=True)


if __name__ == "__main__":
    if len(sys.argv) != 3:
        sys.exit("usage: record.py <adroit binary> <out.cast>")
    record(sys.argv[1], sys.argv[2])
