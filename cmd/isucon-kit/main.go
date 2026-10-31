package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Build   []string            `yaml:"build"`
	Hosts   []Host              `yaml:"hosts"`
	Analyze map[string]Analyzer `yaml:"analyze"`
}

type Host struct {
	SSH     string            `yaml:"ssh"`
	Files   map[string]string `yaml:"files"` // local path -> remote path
	Restart []string          `yaml:"restart"`
}

type Analyzer struct {
	Log string   `yaml:"log"`
	Cmd []string `yaml:"cmd"`
}

func main() {
	config := flag.String("c", "isucon-kit.yaml", "config file")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: isucon-kit [-c config] deploy | status | analyze <name>")
	}
	flag.Parse()
	if err := run(*config, flag.Args()); err != nil {
		fmt.Fprintln(os.Stderr, "isucon-kit:", err)
		os.Exit(1)
	}
}

func run(path string, args []string) error {
	cfg, err := load(path)
	if err != nil {
		return err
	}
	// Local paths in the config are relative to the config file.
	if err := os.Chdir(filepath.Dir(path)); err != nil {
		return err
	}
	switch {
	case len(args) == 1 && args[0] == "deploy":
		return deploy(cfg)
	case len(args) == 1 && args[0] == "status":
		return status(cfg)
	case len(args) == 2 && args[0] == "analyze":
		return analyze(cfg, args[1])
	}
	flag.Usage()
	os.Exit(2)
	return nil
}

func load(path string) (Config, error) {
	var cfg Config
	f, err := os.Open(path)
	if err != nil {
		return cfg, err
	}
	defer f.Close()
	d := yaml.NewDecoder(f)
	d.KnownFields(true)
	if err := d.Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("%s: %w", path, err)
	}
	if len(cfg.Hosts) == 0 {
		return cfg, fmt.Errorf("%s: no hosts", path)
	}
	for name, a := range cfg.Analyze {
		if a.Log == "" || len(a.Cmd) == 0 {
			return cfg, fmt.Errorf("%s: analyze.%s needs log and cmd", path, name)
		}
	}
	return cfg, nil
}

func deploy(cfg Config) error {
	if len(cfg.Build) > 0 {
		if err := attach(exec.Command(cfg.Build[0], cfg.Build[1:]...)).Run(); err != nil {
			return fmt.Errorf("build: %w", err)
		}
	}
	var logs []string
	for _, a := range cfg.Analyze {
		logs = append(logs, a.Log)
	}
	sort.Strings(logs)
	for _, h := range cfg.Hosts {
		fmt.Printf("== %s\n", h.SSH)
		locals := keys(h.Files)
		modes := make([]os.FileMode, len(locals))
		for i, local := range locals {
			info, err := os.Stat(local)
			if err != nil {
				return err
			}
			modes[i] = info.Mode().Perm()
			if err := attach(exec.Command("scp", "-q", "--", local, fmt.Sprintf("%s:/tmp/isucon-kit.%d", h.SSH, i))).Run(); err != nil {
				return fmt.Errorf("%s: upload %s: %w", h.SSH, local, err)
			}
		}
		if err := attach(exec.Command("ssh", h.SSH, deployScript(h, modes, logs))).Run(); err != nil {
			return fmt.Errorf("%s: %w", h.SSH, err)
		}
	}
	return nil
}

// Logs are truncated on every deploy so the next bench is analyzed alone.
func deployScript(h Host, modes []os.FileMode, logs []string) string {
	lines := []string{"set -e"}
	for i, local := range keys(h.Files) {
		tmp := fmt.Sprintf("/tmp/isucon-kit.%d", i)
		// install replaces the file instead of writing into it, so a running binary is not "text file busy".
		lines = append(lines, fmt.Sprintf("sudo install -m %o %s %s && rm -f %s", modes[i], tmp, quote(h.Files[local]), tmp))
	}
	for _, l := range logs {
		lines = append(lines, fmt.Sprintf("if sudo test -f %[1]s; then sudo truncate -s 0 %[1]s; fi", quote(l)))
	}
	if len(h.Restart) > 0 {
		units := make([]string, len(h.Restart))
		for i, u := range h.Restart {
			units[i] = quote(u)
		}
		lines = append(lines, "sudo systemctl restart "+strings.Join(units, " "))
	}
	return strings.Join(lines, "\n")
}

func status(cfg Config) error {
	var failed error
	for _, h := range cfg.Hosts {
		fmt.Printf("== %s\n", h.SSH)
		locals := keys(h.Files)
		remotes := make([]string, len(locals))
		for i, local := range locals {
			remotes[i] = quote(h.Files[local])
		}
		// sha256sum exits non-zero when a file is missing; that shows up as "missing" below.
		out, err := exec.Command("ssh", h.SSH, "sudo sha256sum -- "+strings.Join(remotes, " ")).Output()
		var exit *exec.ExitError
		if err != nil && !(errors.As(err, &exit) && exit.ExitCode() == 1) {
			fmt.Printf("  unreachable: %v\n", err)
			failed = errors.New("some hosts could not be checked")
			continue
		}
		remote := map[string]string{}
		sc := bufio.NewScanner(bytes.NewReader(out))
		for sc.Scan() {
			if hash, path, ok := strings.Cut(sc.Text(), "  "); ok {
				remote[path] = hash
			}
		}
		for _, local := range locals {
			b, err := os.ReadFile(local)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(b)
			state := "ok"
			switch r, ok := remote[h.Files[local]]; {
			case !ok:
				state = "missing"
			case r != hex.EncodeToString(sum[:]):
				state = "DIFF"
			}
			fmt.Printf("  %-7s %s\n", state, h.Files[local])
		}
	}
	return failed
}

func analyze(cfg Config, name string) error {
	a, ok := cfg.Analyze[name]
	if !ok {
		return fmt.Errorf("unknown analyzer %q", name)
	}
	for _, h := range cfg.Hosts {
		fmt.Printf("== %s\n", h.SSH)
		src := exec.Command("ssh", h.SSH, "sudo cat -- "+quote(a.Log))
		src.Stderr = os.Stderr
		pipe, err := src.StdoutPipe()
		if err != nil {
			return err
		}
		dst := attach(exec.Command(a.Cmd[0], a.Cmd[1:]...))
		dst.Stdin = pipe
		if err := src.Start(); err != nil {
			return err
		}
		dstErr := dst.Run()
		// A host without this log (e.g. the DB host for alp) is skipped, not fatal.
		if err := src.Wait(); err != nil {
			fmt.Fprintf(os.Stderr, "%s: read %s: %v\n", h.SSH, a.Log, err)
		}
		if dstErr != nil {
			return fmt.Errorf("%s: %w", a.Cmd[0], dstErr)
		}
	}
	return nil
}

func attach(c *exec.Cmd) *exec.Cmd {
	c.Stdout, c.Stderr = os.Stdout, os.Stderr
	return c
}

func keys(m map[string]string) []string {
	k := make([]string, 0, len(m))
	for s := range m {
		k = append(k, s)
	}
	sort.Strings(k)
	return k
}

func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
