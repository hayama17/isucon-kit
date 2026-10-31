package main

import (
	"os"
	"os/exec"
	"testing"
)

func TestDeployScript(t *testing.T) {
	h := Host{
		Files:   map[string]string{"b": "/etc/nginx/nginx.conf", "a": "/home/isucon/it's app"},
		Restart: []string{"app.service", "nginx"},
	}
	got := deployScript(h, []os.FileMode{0o755, 0o644}, []string{"/var/log/nginx/access.log"})
	want := `set -e
sudo install -m 755 /tmp/isucon-kit.0 '/home/isucon/it'\''s app' && rm -f /tmp/isucon-kit.0
sudo install -m 644 /tmp/isucon-kit.1 '/etc/nginx/nginx.conf' && rm -f /tmp/isucon-kit.1
if sudo test -f '/var/log/nginx/access.log'; then sudo truncate -s 0 '/var/log/nginx/access.log'; fi
sudo systemctl restart 'app.service' 'nginx'`
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if err := exec.Command("sh", "-n", "-c", got).Run(); err != nil {
		t.Fatalf("not valid sh: %v", err)
	}
}

func TestQuoteRoundTrip(t *testing.T) {
	s := `a'b "c" $d`
	out, err := exec.Command("sh", "-c", "printf %s "+quote(s)).Output()
	if err != nil || string(out) != s {
		t.Fatalf("got %q, %v", out, err)
	}
}
