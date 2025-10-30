package main

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"9fans.net/go/plumb"
)

func (e *Exec) verbis(obj Object, m *plumb.Message, r *Rule) bool {
	switch obj {
	default:
		errorf("unimplemented 'is' object %d\n", obj)
	case OData:
		return string(m.Data) == r.qarg
	case ODst:
		return string(m.Dst) == r.qarg
	case OType:
		return string(m.Type) == r.qarg
	case OWdir:
		return string(m.Dir) == r.qarg
	case OSrc:
		return string(m.Src) == r.qarg
	}
	return false
}

// Match re on text, returning the match when it includes clickoffset
// Clickoffset is in runes, not bytes
func clickmatch(re *regexp.Regexp, text string, clickoffset int) (ri []int, rs []string) {
	// It's a little gross that there is no library function to do this count?
	i := 0
	clickp := 0
	for clickp = range text {
		if i == clickoffset {
			break
		}
		i++
	}

	for i := range text[0:clickp] {
		if ri := re.FindStringSubmatchIndex(text[i:]); ri != nil {
			if ri[0] <= clickp && clickp <= ri[1] {
				return ri, re.FindStringSubmatch(text[i:])
			}
		}
	}
	return nil, nil
}

func (e *Exec) setvar(rs []string) {
	for i := 0; i < len(e.match); i++ {
		if len(rs) > i {
			e.match[i] = rs[i]
		} else {
			e.match[i] = ""
		}
	}
}

func (e *Exec) verbmatches(obj Object, m *plumb.Message, r *Rule) bool {
	text := ""
	switch obj {
	default:
		errorf("unimplemented 'matches' object %d\n", obj)
	case OData:
		clickval := m.LookupAttr("click")
		if clickval == "" {
			text = string(m.Data)
		} else {
			cval, err := strconv.Atoi(clickval)
			if err != nil {
				errorf("error parsing clickval (%v), using 0", clickval)
			}
			ri, rs := clickmatch(r.regex, string(m.Data), cval)
			if ri == nil {
				return false
			}
			p0, p1 := int(ri[0]), int(ri[1])
			if e.p0 >= 0 && !(p0 == e.p0 && p1 == e.p1) {
				return false
			}
			e.clearclick = true
			e.setdata = true
			e.p0 = p0
			e.p1 = p1
			e.setvar(rs)
			return true
		}
	case ODst:
		text = m.Dst
	case OType:
		text = m.Type
	case OWdir:
		text = m.Dir
	case OSrc:
		text = m.Src
	}
	/* must match full text */

	if ri := r.regex.FindStringSubmatchIndex(text); ri == nil || ri[0] != 0 || ri[1] != len(text) {
		return false
	} else {
		rs := r.regex.FindStringSubmatch(text)
		e.setvar(rs)
		return true
	}
}

func isfile(file string, maskon uint32, maskoff uint32) bool {
	fi, err := os.Stat(file)
	if err != nil {
		return false
	}

	mode := uint32(fi.Mode())
	if (mode & maskon) == 0 {
		return false
	}
	if (mode & maskoff) != 0 {
		return false
	}
	return true
}

func absolute(dir, file string) string {
	if filepath.IsAbs(file) {
		return file
	}
	path := filepath.Join(dir, file)
	return filepath.Clean(path)
}

func (e *Exec) verbisfile(obj Object, m *plumb.Message, r *Rule, maskon, maskoff uint32) (string, bool) {
	file := ""
	switch obj {
	default:
		errorf("unimplemented 'isfile' object %d", obj)
	case OArg:
		file = absolute(m.Dir, expand(e, []rune(r.arg)))
		if isfile(file, maskon, maskoff) {
			return file, true
		}
	case OData:
		file = absolute(m.Dir, expand(e, []rune(string(m.Data))))
		if isfile(file, maskon, maskoff) {
			return file, true
		}
	case OWdir:
		file = absolute(m.Dir, expand(e, []rune(string(m.Dir))))
		if isfile(file, maskon, maskoff) {
			return file, true
		}
	}
	return "", false
}

func (e *Exec) verbset(obj Object, m *plumb.Message, r *Rule) bool {
	switch obj {
	default:
		errorf("unimplemented 'set' object %d", obj)
		return false
	case OData:
		m.Data = []byte(expand(e, []rune(r.arg)))
		e.p0 = -1
		e.p1 = -1
		e.setdata = false
		return true
	case ODst:
		m.Dst = expand(e, []rune(r.arg))
		return true
	case OType:
		m.Type = expand(e, []rune(r.arg))
		return true
	case OWdir:
		m.Dir = expand(e, []rune(r.arg))
		return true
	case OSrc:
		m.Src = expand(e, []rune(r.arg))
		return true
	}
}

func (e *Exec) verbadd(obj Object, m *plumb.Message, r *Rule) bool {
	switch obj {
	default:
		errorf("unimplemented 'add' object %d", obj)
		return false
	case OAttr:
		m.Attr = readAttr([]byte(expand(e, []rune(r.arg))))
		return true
	}
}

func (e *Exec) verbdelete(obj Object, m *plumb.Message, r *Rule) bool {
	switch obj {
	default:
		errorf("unimplemented 'delete' object %d", obj)
		return false
	case OAttr:
		a := expand(e, []rune(r.arg))
		m.Attr = plumbdelattr(m.Attr, a)
		return true
	}
}

func plumbdelattr(attr *plumb.Attribute, name string) *plumb.Attribute {
	var prev, l *plumb.Attribute
	for l = attr; l != nil; l = l.Next {
		if name == l.Name {
			break
		}
		prev = l
	}
	if l == nil {
		return attr
	}
	if prev != nil {
		prev.Next = l.Next
	} else {
		attr = l.Next
	}
	return attr
}

func (e *Exec) matchpat(m *plumb.Message, r *Rule) bool {
	switch r.verb {
	default:
		errorf("unimplemented verb %d\n", r.verb)
		return false
	case VAdd:
		return e.verbadd(r.obj, m, r)
	case VDelete:
		return e.verbdelete(r.obj, m, r)
	case VIs:
		return e.verbis(r.obj, m, r)
	case VIsdir:
		dir, ok := e.verbisfile(r.obj, m, r, uint32(fs.ModeDir), 0)
		if ok {
			e.dir = dir
		}
		return ok
	case VIsfile:
		file, ok := e.verbisfile(r.obj, m, r, ^uint32(fs.ModeDir), uint32(fs.ModeDir))
		if ok {
			e.file = file
		}
		return ok
	case VMatches:
		return e.verbmatches(r.obj, m, r)
	case VSet:
		e.verbset(r.obj, m, r)
		return true
	}
}

func (e *Exec) rewrite(m *plumb.Message) {
	if e.clearclick {
		m.Attr = plumbdelattr(m.Attr, "click")
		if e.setdata {
			m.Data = []byte(expand(e, []rune("$0")))
		}
	}
}

func (e *Exec) buildargv(s string) []string {
	fields := strings.Fields(s)
	for i, f := range fields {
		fields[i] = expand(e, []rune(f))
	}
	return fields
}

func newexec(m *plumb.Message) *Exec {
	return &Exec{msg: m, p0: -1, p1: -1}
}

func matchruleset(m *plumb.Message, rs *Ruleset) *Exec {
	if m.Dst != "" && rs.port != "" && m.Dst != rs.port {
		return nil
	}
	exec := newexec(m)
	for _, p := range rs.pat {
		if !exec.matchpat(m, &p) {
			return nil
		}
	}
	if rs.port != "" && m.Dst == "" {
		m.Dst = rs.port
	}
	exec.rewrite(m)
	return exec
}

// Launch the required app.
func startup(rs *Ruleset, e *Exec) string {
	var a Rule
	launch := func(a *Rule) string {
		argv := e.buildargv(a.arg)
		if len(argv) == 0 {
			return "empty argument list"
		}
		cmd := exec.Command(argv[0], argv[1:]...)
		os.Stdin = nil
		cmd.Start()
		return ""
	}

	if rs != nil {
		for _, a = range rs.act {
			if a.verb == VStart {
				return launch(&a)
			}
			if a.verb == VClient {
				if e.msg.Dst == "" {
					return "no port for \"client\" rule"
				}
				e.holdforclient = true
				return launch(&a)
			}
		}
	}
	return "no start action for plumb message"
}
