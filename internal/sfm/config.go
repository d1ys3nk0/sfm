package sfm

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

type Config struct {
	Vault   string `toml:"vault"`
	Targets struct {
		Patterns []string `toml:"patterns"`
	} `toml:"targets"`
	home   string
	rules  []rule
	source string
	raw    []byte
}
type rule struct {
	include       bool
	ns, literal   string
	wildcard, dir bool
	re            *regexp.Regexp
}

func expand(s, home string) string {
	if s == "~" {
		return home
	}
	if strings.HasPrefix(s, "~/") {
		return filepath.Join(home, s[2:])
	}
	return s
}
func readConfig(path string) (*Config, error) {
	b, e := os.ReadFile(path)
	if e != nil {
		return nil, e
	}
	resolved, e := filepath.EvalSymlinks(path)
	if e != nil {
		return nil, e
	}
	if e = safe(resolved); e != nil {
		return nil, e
	}
	c := &Config{source: resolved, raw: b}
	d := toml.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if e = d.Decode(c); e != nil {
		return nil, e
	}
	c.home, e = os.UserHomeDir()
	if e != nil {
		return nil, e
	}
	if c.Vault == "" {
		return nil, fmt.Errorf("vault is required")
	}
	c.Vault, e = filepath.Abs(expand(c.Vault, c.home))
	if e != nil {
		return nil, e
	}
	if c.Vault == c.home || c.Vault == "/" {
		return nil, fmt.Errorf("vault cannot be home or root")
	}
	if e = safe(c.Vault); e != nil {
		return nil, e
	}
	if st, e := os.Lstat(c.Vault); e == nil && (!st.IsDir() || st.Mode()&os.ModeSymlink != 0) {
		return nil, fmt.Errorf("vault must be a directory")
	}
	for _, s := range c.Targets.Patterns {
		r, e := compile(s, c.home, c.Vault)
		if e != nil {
			return nil, e
		}
		if r != nil {
			c.rules = append(c.rules, *r)
		}
	}
	return c, nil
}
func compile(raw, home, vault string) (*rule, error) {
	// Unescaped trailing spaces are formatting; escaped spaces are literal.
	for strings.HasSuffix(raw, " ") {
		i := len(raw) - 2
		slashes := 0
		for i >= 0 && raw[i] == '\\' {
			slashes++
			i--
		}
		if slashes%2 == 1 {
			break
		}
		raw = raw[:len(raw)-1]
	}
	if raw == "" || strings.HasPrefix(raw, "#") {
		return nil, nil
	}
	r := &rule{include: true, ns: "home"}
	s := raw
	if strings.HasPrefix(s, "!") {
		r.include = false
		s = s[1:]
	}
	if strings.HasPrefix(s, "~/") {
		s = s[2:]
	} else if strings.HasPrefix(s, "/") {
		r.ns = "root"
		s = s[1:]
		if within("/"+strings.TrimSuffix(s, "/"), home) {
			return nil, fmt.Errorf("use ~/ for home targets")
		}
	}
	if s == "" {
		return nil, fmt.Errorf("empty target")
	}
	for _, part := range strings.Split(strings.TrimSuffix(s, "/"), "/") {
		if part == ".." || part == "." || part == "" {
			return nil, fmt.Errorf("unsafe target: %s", raw)
		}
	}
	r.dir = strings.HasSuffix(s, "/")
	s = strings.TrimSuffix(s, "/")
	r.literal = s
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
			if i == len(s) {
				return nil, fmt.Errorf("unfinished escape: %s", raw)
			}
			b.WriteString(regexp.QuoteMeta(s[i : i+1]))
		case '*':
			r.wildcard = true
			if i+1 < len(s) && s[i+1] == '*' && (i == 0 || s[i-1] == '/') && (i+2 == len(s) || s[i+2] == '/') {
				i++
				if i+1 < len(s) && s[i+1] == '/' {
					i++
					b.WriteString("(?:.*/)?")
				} else {
					b.WriteString(".*")
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			r.wildcard = true
			b.WriteString("[^/]")
		case '[':
			r.wildcard = true
			j := i + 1
			if j < len(s) && (s[j] == '!' || s[j] == '^') {
				j++
			}
			if j < len(s) && s[j] == ']' {
				j++
			}
			for j < len(s) && s[j] != ']' {
				j++
			}
			if j == len(s) {
				b.WriteString("\\[")
			} else {
				v := s[i+1 : j]
				if strings.HasPrefix(v, "!") {
					v = "^" + v[1:]
				}
				b.WriteString("[" + classBody(v) + "]")
				i = j
			}
		default:
			b.WriteString(regexp.QuoteMeta(s[i : i+1]))
		}
	}
	if r.dir {
		b.WriteString("/.*$")
	} else {
		b.WriteString("(?:/.*)?$")
	}
	var e error
	r.re, e = regexp.Compile(b.String())
	if e != nil {
		return nil, e
	}
	if !r.wildcard {
		base := home
		if r.ns == "root" {
			base = "/"
		}
		if r.ns == "home" && within(unescape(s), "_") {
			return nil, fmt.Errorf("reserved home target: %s; ~/_ is reserved for root payloads", raw)
		}
		candidate := filepath.Join(base, unescape(s))
		if within(vault, candidate) || within(candidate, vault) {
			return nil, fmt.Errorf("target selects vault: %s", raw)
		}
	}
	return r, nil
}
func unescape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
func within(path, base string) bool {
	if base == "/" {
		return strings.HasPrefix(path, "/")
	}
	return path == base || strings.HasPrefix(path, base+"/")
}
func (c *Config) name(path string) string {
	if path == c.home {
		return "home/"
	}
	if within(path, c.home) {
		return "home/" + strings.TrimPrefix(path, c.home+"/")
	}
	return "root/" + strings.TrimPrefix(path, "/")
}
func (c *Config) destination(name string) (string, error) {
	parts := strings.SplitN(name, "/", 2)
	if strings.ContainsAny(name, "\r\n") || len(parts) != 2 || parts[1] == "" || parts[1] == "." || parts[1] == ".." || filepath.Clean(parts[1]) != parts[1] || strings.HasPrefix(parts[1], "/") || strings.HasPrefix(parts[1], "../") {
		return "", fmt.Errorf("unsafe vault path: %s", name)
	}
	base := c.home
	switch parts[0] {
	case "home":
		if within(parts[1], "_") {
			return "", fmt.Errorf("reserved home target: %s; ~/_ is reserved for root payloads", name)
		}
	case "root":
		base = "/"
		if within("/"+parts[1], c.home) {
			return "", fmt.Errorf("root/home alias: %s", name)
		}
	default:
		return "", fmt.Errorf("invalid namespace: %s", name)
	}
	p := filepath.Join(base, parts[1])
	if within(p, c.Vault) {
		return "", fmt.Errorf("vault self-selection: %s", name)
	}
	return p, nil
}

// vaultPath maps validated internal identifiers to the directory-only vault.
func (c *Config) vaultPath(name string) string {
	if strings.HasPrefix(name, "home/") {
		return filepath.Join(c.Vault, strings.TrimPrefix(name, "home/"))
	}
	return filepath.Join(c.Vault, "_", strings.TrimPrefix(name, "root/"))
}

// vaultName is the inverse of vaultPath; empty names are structural directories.
func (c *Config) vaultName(path string) (string, error) {
	rel, e := filepath.Rel(c.Vault, path)
	if e != nil {
		return "", e
	}
	if rel == "." || rel == "_" {
		return "", nil
	}
	name := "home/" + rel
	if strings.HasPrefix(rel, "_/") {
		name = "root/" + strings.TrimPrefix(rel, "_/")
	}
	if _, e = c.destination(name); e != nil {
		return "", e
	}
	return name, nil
}

func (c *Config) selected(name string, dir bool) bool {
	parts := strings.SplitN(name, "/", 2)
	if len(parts) != 2 {
		return false
	}
	value := false
	for _, r := range c.rules {
		if r.ns != parts[0] || !r.matches(parts[1], dir) {
			continue
		}
		value = r.include
	}
	return value
}

// StateDir returns the private reconciliation directory for a canonical vault path.
func StateDir(vault string) (string, error) {
	v, e := filepath.Abs(vault)
	if e != nil {
		return "", e
	}
	if resolved, e := filepath.EvalSymlinks(v); e == nil {
		v = resolved
	}
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		h, e := os.UserHomeDir()
		if e != nil {
			return "", e
		}
		base = filepath.Join(h, ".local/state")
	}
	sum := sha256.Sum256([]byte(v))
	return filepath.Join(base, "sfm", fmt.Sprintf("%x", sum)), nil
}

func (r rule) matches(relative string, dir bool) bool {
	if dir {
		relative += "/"
	}
	return r.re.MatchString(relative)
}
func (c *Config) prune(name string) bool {
	if c.selected(name, true) {
		return false
	}
	for _, r := range c.rules {
		if !r.include {
			continue
		}
		parts := strings.SplitN(name, "/", 2)
		if len(parts) == 2 && r.ns == parts[0] && (r.wildcard || strings.HasPrefix(unescape(r.literal), parts[1]+"/")) {
			return false
		}
	}
	return true
}

func wildcardPrefix(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' {
			i++
			continue
		}
		if strings.ContainsRune("*?[", rune(s[i])) {
			return s[:i]
		}
	}
	return s
}

func classBody(v string) string {
	var b strings.Builder
	for i := 0; i < len(v); i++ {
		ch := v[i]
		if ch == '\\' && i+1 < len(v) {
			i++
			if strings.ContainsRune("-]^\\", rune(v[i])) {
				b.WriteByte('\\')
			}
			b.WriteByte(v[i])
			continue
		}
		if ch == '[' || ch == ']' {
			b.WriteByte('\\')
		}
		b.WriteByte(ch)
	}
	return b.String()
}
