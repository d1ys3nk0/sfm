package sfm

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
)

type policyValue struct {
	value      string
	start, end int
}
type policyArray struct {
	start, end int
	values     []policyValue
}

func policySpan(raw []byte) (policyArray, error) {
	var p unstable.Parser
	p.KeepComments = true
	p.Reset(raw)
	table := []string{}
	for p.NextExpression() {
		n := p.Expression()
		if n.Kind == unstable.Table {
			table = keys(n)
			continue
		}
		if n.Kind != unstable.KeyValue {
			continue
		}
		key := append(append([]string{}, table...), keys(n)...)
		if strings.Join(key, ".") != "targets.patterns" {
			continue
		}
		v := n.Value()
		if v.Kind != unstable.Array {
			return policyArray{}, fmt.Errorf("targets.patterns must be an array")
		}
		keyNodes := n.Key()
		last := 0
		for keyNodes.Next() {
			r := keyNodes.Node().Raw
			last = int(r.Offset + r.Length)
		}
		start := last + bytes.IndexByte(raw[last:], '[')
		a := policyArray{start: start}
		last = start + 1
		children := v.Children()
		for children.Next() {
			node := children.Node()
			r := node.Raw
			if node.Kind == unstable.String {
				a.values = append(a.values, policyValue{string(node.Data), int(r.Offset), int(r.Offset + r.Length)})
			}
			if int(r.Offset+r.Length) > last {
				last = int(r.Offset + r.Length)
			}
		}
		end := last + bytes.IndexByte(raw[last:], ']')
		if end < last {
			return a, fmt.Errorf("cannot locate policy array end")
		}
		a.end = end
		return a, nil
	}
	if e := p.Error(); e != nil {
		return policyArray{}, e
	}
	return policyArray{start: -1}, nil
}
func keys(n *unstable.Node) []string {
	var k []string
	i := n.Key()
	for i.Next() {
		k = append(k, string(i.Node().Data))
	}
	return k
}
func decodePolicy(raw []byte, c *Config) error {
	if e := toml.Unmarshal(raw, c); e != nil {
		return e
	}
	c.rules = nil
	for _, v := range c.Targets.Patterns {
		r, e := compile(v, c.home, c.Vault)
		if e != nil {
			return e
		}
		if r != nil {
			c.rules = append(c.rules, *r)
		}
	}
	return nil
}
func editPolicy(c *Config, name string, dir, adding bool, pattern string) ([]byte, error) {
	a, e := policySpan(c.raw)
	if e != nil {
		return nil, e
	}
	if a.start < 0 {
		if len(c.Targets.Patterns) > 0 {
			return nil, fmt.Errorf("cannot locate selection policy")
		}
		return newPolicyArray(c.raw, pattern)
	}
	remove := map[int]bool{}
	duplicate := false
	for i, v := range a.values {
		r, e := compile(v.value, c.home, c.Vault)
		if e != nil {
			return nil, e
		}
		if r == nil || r.wildcard {
			continue
		}
		candidate := r.ns + "/" + unescape(r.literal)
		if adding {
			if candidate == name && !r.include {
				remove[i] = true
			}
			if candidate == name && r.include && r.dir == dir {
				duplicate = true
			}
		} else if candidate == name && r.include || dir && candidate != name && within(candidate, name) {
			remove[i] = true
		}
	}
	raw := append([]byte{}, c.raw...)
	type span struct{ start, end int }
	var spans []span
	for i := range remove {
		v := a.values[i]
		end := v.end
		next := a.end
		if i+1 < len(a.values) {
			next = a.values[i+1].start
		}
		comment := false
		for j := end; j < next; j++ {
			if comment {
				if raw[j] == '\n' {
					comment = false
				}
				continue
			}
			if raw[j] == '#' {
				comment = true
				continue
			}
			if raw[j] == ',' {
				spans = append(spans, span{j, j + 1})
				break
			}
		}
		spans = append(spans, span{v.start, v.end})
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start > spans[j].start })
	for _, v := range spans {
		raw = append(raw[:v.start], raw[v.end:]...)
	}
	a, e = policySpan(raw)
	if e != nil {
		return nil, e
	}
	next := &Config{Vault: c.Vault, home: c.home}
	if e = decodePolicy(raw, next); e != nil {
		return nil, e
	}
	if adding && duplicate {
		if dir || next.selected(name, false) {
			return raw, nil
		} // Move a shadowed literal file inclusion to the end.
		return moveLiteral(c, pattern)

	}
	if !adding {
		broader := next.selected(name, dir)
		if dir && !broader {
			for _, r := range next.rules {
				if r.include && r.wildcard && r.ns == strings.SplitN(name, "/", 2)[0] {
					prefix := strings.TrimSuffix(unescape(wildcardPrefix(r.literal)), "/")
					relative := strings.SplitN(name, "/", 2)[1]
					if prefix != "" && (within(relative, prefix) || within(prefix, relative)) || prefix == "" && strings.Contains(r.literal, "/") {
						broader = true
					}
				}
			}
		}
		if !broader {
			return raw, nil
		}
		for _, v := range a.values {
			if v.value == pattern {
				return raw, nil
			}
		}
	}
	insert := a.end
	prefix := ""
	if adding && dir {
		for _, v := range a.values {
			if strings.HasPrefix(v.value, "!") {
				insert = v.start
				break
			}
		}
	}
	if insert < a.end {
		return splice(raw, insert, strconv.Quote(pattern)+", "), nil
	}
	if len(a.values) > 0 {
		last := a.values[len(a.values)-1]
		between := raw[last.end:a.end]
		comma := false
		comment := false
		for _, ch := range between {
			if comment {
				if ch == '\n' {
					comment = false
				}
				continue
			}
			if ch == '#' {
				comment = true
			}
			if ch == ',' && !comment {
				comma = true
			}
		}
		if !comma {
			raw = splice(raw, last.end, ",")
			a.end++
		}
	}
	if bytes.Contains(raw[a.start:a.end], []byte("\n")) {
		prefix = "\n  "
		return splice(raw, a.end, prefix+strconv.Quote(pattern)+",\n"), nil
	}
	if len(a.values) > 0 {
		prefix = " "
	}
	return splice(raw, a.end, prefix+strconv.Quote(pattern)), nil
}
func splice(raw []byte, offset int, s string) []byte {
	b := make([]byte, 0, len(raw)+len(s))
	b = append(b, raw[:offset]...)
	b = append(b, s...)
	return append(b, raw[offset:]...)
}
func moveLiteral(c *Config, pattern string) ([]byte, error) { // Remove the shadowed literal through the AST, then append it once.
	a, e := policySpan(c.raw)
	if e != nil {
		return nil, e
	}
	raw := append([]byte{}, c.raw...)
	for _, v := range a.values {
		if v.value != pattern {
			continue
		}
		end := v.end
		for end < len(raw) && raw[end] != ',' && raw[end] != ']' && raw[end] != '#' {
			end++
		}
		if end < len(raw) && raw[end] == ',' {
			end++
		} else {
			end = v.end
		}
		raw = append(raw[:v.start], raw[end:]...)
		break
	}
	next := *c
	next.raw = raw
	next.Targets.Patterns = nil
	return editPolicy(&next, "", false, true, pattern)
}

func newPolicyArray(raw []byte, pattern string) ([]byte, error) {
	var p unstable.Parser
	p.Reset(raw)
	for p.NextExpression() {
		n := p.Expression()
		if n.Kind == unstable.Table && strings.Join(keys(n), ".") == "targets" {
			last := 0
			k := n.Key()
			for k.Next() {
				r := k.Node().Raw
				last = int(r.Offset + r.Length)
			}
			end := last + bytes.IndexByte(raw[last:], ']') + 1
			newline := bytes.IndexByte(raw[end:], '\n')
			if newline >= 0 {
				return splice(raw, end+newline+1, "patterns = ["+strconv.Quote(pattern)+"]\n"), nil
			}
			return append(raw, []byte("\npatterns = ["+strconv.Quote(pattern)+"]\n")...), nil
		}
		if n.Kind == unstable.KeyValue && strings.Join(keys(n), ".") == "targets" {
			return nil, fmt.Errorf("add targets.patterns to the inline table before editing selections")
		}
	}
	if e := p.Error(); e != nil {
		return nil, e
	}
	return append(raw, []byte("\n[targets]\npatterns = ["+strconv.Quote(pattern)+"]\n")...), nil
}
