package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"strconv"
	"strings"
	"sync"
	"time"

	"9fans.net/go/plan9"
	"9fans.net/go/plumb"
	// "github.com/paul-lalonde/plumb"
)

var Ebadfcall = "bad fcall type"
var Eperm = "permission denied"
var Enomem = "malloc failed for buffer"
var Enotdir = "not a directory"
var Enoexist = "plumb file does not exist"
var Eisdir = "file is a directory"
var Ebadmsg = "bad plumb message format"
var Enosuchport = "no such plumb port"
var Enoport = "couldn't find destination for message"
var Einuse = "file already open"

type Dirtab struct {
	name  string
	typ   byte
	qid   uint64
	perm  uint32
	nopen int /* #open fids this on port */
	fopen *Fid
	holdq *Holdq
	readq *Readreq
	sendq *Sendreq
}

type Fid struct {
	fid      uint32
	busy     bool
	open     bool
	mode     byte
	qid      plan9.Qid
	dir      *Dirtab
	offset   int64  /* at zeroed of beginning message each, or read write */
	writebuf []byte /* message partial so written far; tells offset much how */
	//next     *Fid
	nextopen *Fid
}

type Readreq struct {
	fid   *Fid
	fcall *plan9.Fcall
	buf   []byte
	next  *Readreq
}

type Sendreq struct {
	nfid  int    /* number of fids that should receive this message */
	nleft int    /* number left that haven't received it */
	fid   []*Fid /* fid[nfid] */
	msg   *plumb.Message
	pack  []byte /* plumbpack()ed message */
	next  *Sendreq
}

type Holdq struct {
	msg  *plumb.Message
	next *Holdq
}

const (
	NDIR  = 50
	Nhash = 16

	Qdir   = 0
	Qrules = 1
	Qsend  = 2
	Qport  = 3
	NQID   = Qport
)

type Fsys struct {
	queue         sync.Mutex
	rulesref      sync.Mutex
	rulesrefcount int
	readlock      sync.Mutex
	fcall         map[uint8]func(*plan9.Fcall, []byte, *Fid) *plan9.Fcall
	messagesize   uint
	clock         uint32

	sfdR, sfdW *os.File

	fids map[uint32]*Fid

	rules Rules

	user string

	text []byte // unparsed text during writerules
}

func NewFsys() *Fsys {
	fsys := &Fsys{}

	fsys.fcall = map[uint8]func(*plan9.Fcall, []byte, *Fid) *plan9.Fcall{
		plan9.Tflush:   func(t *plan9.Fcall, buf []byte, fid *Fid) *plan9.Fcall { return fsys.flush(t, buf, fid) },
		plan9.Tversion: func(t *plan9.Fcall, buf []byte, fid *Fid) *plan9.Fcall { return fsys.version(t, buf, fid) },
		plan9.Tauth:    func(t *plan9.Fcall, buf []byte, fid *Fid) *plan9.Fcall { return fsys.auth(t, buf, fid) },
		plan9.Tattach:  func(t *plan9.Fcall, buf []byte, fid *Fid) *plan9.Fcall { return fsys.attach(t, buf, fid) },
		plan9.Twalk:    func(t *plan9.Fcall, buf []byte, fid *Fid) *plan9.Fcall { return fsys.walk(t, buf, fid) },
		plan9.Topen:    func(t *plan9.Fcall, buf []byte, fid *Fid) *plan9.Fcall { return fsys.open(t, buf, fid) },
		plan9.Tcreate:  func(t *plan9.Fcall, buf []byte, fid *Fid) *plan9.Fcall { return fsys.create(t, buf, fid) },
		plan9.Tread:    func(t *plan9.Fcall, buf []byte, fid *Fid) *plan9.Fcall { return fsys.read(t, buf, fid) },
		plan9.Twrite:   func(t *plan9.Fcall, buf []byte, fid *Fid) *plan9.Fcall { return fsys.write(t, buf, fid) },
		plan9.Tclunk:   func(t *plan9.Fcall, buf []byte, fid *Fid) *plan9.Fcall { return fsys.clunk(t, buf, fid) },
		plan9.Tremove:  func(t *plan9.Fcall, buf []byte, fid *Fid) *plan9.Fcall { return fsys.remove(t, buf, fid) },
		plan9.Tstat:    func(t *plan9.Fcall, buf []byte, fid *Fid) *plan9.Fcall { return fsys.stat(t, buf, fid) },
		plan9.Twstat:   func(t *plan9.Fcall, buf []byte, fid *Fid) *plan9.Fcall { return fsys.wstat(t, buf, fid) },
	}

	fsys.fids = map[uint32]*Fid{}
	fsys.rules = newRules()
	cuser, _ := user.Current()
	fsys.user = cuser.Username
	return fsys
}

func (fsys *Fsys) start() {
	fsys.clock = getclock()
	r1, w1, err := os.Pipe()
	if err != nil {
		errorf("can't create pipe")
	}
	r2, w2, err := os.Pipe()
	if err != nil {
		errorf("can't create pipe")
	}
	fsys.sfdR, fsys.sfdW = r1, w2
	if err := post9pservice(r2, w1, "plumb", ""); err != nil {
		errorf("can't post service")
	}
	fsys.proc() // TODO(PAL): Daemonzie
}

func (fsys *Fsys) proc() {
	var f *Fid
	for {
		t, err := plan9.ReadFcall(fsys.sfdR)
		if err != nil {
			errorf("Error reading fcall: %v", err)
			os.Exit(1)
		}
		if fsys.fcall[t.Type] == nil {
			fsys.respond(t, Ebadfcall)
		} else {
			if t.Type == plan9.Tversion || t.Type == plan9.Tauth {
				f = nil
			} else {
				f = fsys.newfid(t.Fid)
			}
			fsys.fcall[t.Type](t, nil, f)
		}
	}
}

func (r *Rules) addport(port string) {
	if port == "" {
		return
	}
	for _, d := range r.dir[NQID:] {
		if d.name == port {
			return
		}
	}
	r.dir = append(r.dir,
		&Dirtab{name: port, qid: uint64(len(r.dir)), perm: 0o0400})
	r.ports = append(r.ports, port)
}

func (rules *Rules) makeports() {
	for _, r := range rules.rs {
		rules.addport(r.port)
	}
}

func (fsys *Fsys) respond(t *plan9.Fcall, msg string) {
	if msg != "" {
		t.Type = plan9.Rerror
		t.Ename = msg
	} else {
		t.Type++
	}
	err := plan9.WriteFcall(fsys.sfdW, t)
	if err != nil {
		errorf("failed to write Fcall: %s", err)
	}
}

func (fsys *Fsys) flush(t *plan9.Fcall, _ []byte, _ *Fid) *plan9.Fcall {
	fsys.queue.Lock()
	for _, d := range fsys.rules.dir[NQID:] {
		flushqueue(d, t.Oldtag)
	}
	fsys.queue.Unlock()
	fsys.respond(t, "")
	return t
}

func flushqueue(d *Dirtab, oldtag uint16) {
	prevr := (*Readreq)(nil)
	for r := d.readq; r != nil; r = r.next {
		if oldtag == r.fcall.Tag {
			if prevr != nil {
				prevr.next = r.next
			} else {
				d.readq = r.next
			}
			return
		}
	}
}

func (fsys *Fsys) version(t *plan9.Fcall, _ []byte, _ *Fid) *plan9.Fcall {
	if t.Msize < 256 {
		fsys.respond(t, "version: message size too small")
	}
	if t.Msize < uint32(fsys.messagesize) {
		fsys.messagesize = uint(t.Msize)
	}
	t.Msize = uint32(fsys.messagesize)
	if t.Version != "9P2000" {
		fsys.respond(t, "unrecognized 9P version")
	}
	t.Version = "9P2000"
	fsys.respond(t, "")
	return t
}

func (fsys *Fsys) auth(t *plan9.Fcall, buf []byte, fid *Fid) *plan9.Fcall {
	fsys.respond(t, "plumber: authentication not required")
	return t
}

func (fsys *Fsys) attach(t *plan9.Fcall, buf []byte, f *Fid) *plan9.Fcall {
	f.busy = true
	f.open = false
	f.qid.Type = plan9.QTDIR
	f.qid.Path = Qdir
	f.qid.Vers = 0
	f.dir = fsys.rules.dir[0]
	out := &plan9.Fcall{
		Type: t.Type,
		Tag:  t.Tag,
		Fid:  f.fid,
		Qid:  f.qid,
	}
	fsys.respond(out, "")
	return t
}

// PAL: the original C uses a fixed set of 16 fid linked lists to manage the hash.  We just use a map.
// fid numbers are provided by the client of the interface, so we need to hash them to our fid.
func (fsys *Fsys) newfid(fid uint32) *Fid {
	fsys.queue.Lock()
	defer fsys.queue.Unlock()

	if _, ok := fsys.fids[fid]; !ok { // Fid doesn't exists, make a new one
		fsys.fids[fid] = &Fid{fid: fid}
	}
	return fsys.fids[fid]
}

func (fsys *Fsys) walk(t *plan9.Fcall, buf []byte, f *Fid) *plan9.Fcall {
	if f.open {
		fsys.respond(t, "clone of an open fid")
		return t
	}

	var nf *Fid
	if t.Fid != t.Newfid {
		nf = fsys.newfid(t.Newfid)
		if nf.busy {
			fsys.respond(t, "clone to a busy fid")
			return t
		}
		nf.busy = true
		nf.open = false
		nf.dir = f.dir
		nf.qid = f.qid
		f = nf
	}
	var out plan9.Fcall
	out.Wqid = []plan9.Qid{}
	dir := f.dir
	q := f.qid
	err := ""

	if len(t.Wname) > 0 {
	NextPath:
		for _, wname := range t.Wname {
			if q.Type&plan9.QTDIR == 0 {
				err = Enotdir
				break
			}
			if wname == ".." {
				q.Type = plan9.QTDIR
				q.Vers = 0
				q.Path = Qdir
				out.Wqid = append(out.Wqid, q)
				continue
			}
			for _, d := range fsys.rules.dir[1:] { // skip '.'
				if wname == d.name {
					q.Type = d.typ
					q.Vers = 0
					q.Path = d.qid
					dir = d
					out.Wqid = append(out.Wqid, q)
					continue NextPath
				}
			}
			err = Enoexist
			break
		}
	}

	out.Type = t.Type
	out.Tag = t.Tag
	if err != "" || len(out.Wqid) < len(t.Wname) {
		if nf != nil {
			nf.busy = false
		}
	} else if len(out.Wqid) == len(t.Wname) {
		f.qid = q
		f.dir = dir
	}

	fsys.respond(&out, err)
	return t
}

func (fsys *Fsys) open(t *plan9.Fcall, buf []byte, f *Fid) *plan9.Fcall {
	deny := func() *plan9.Fcall {
		fsys.respond(t, Eperm)
		return t
	}

	clearrules := false
	if t.Mode&plan9.OTRUNC != 0 {
		if f.qid.Path != Qrules {
			return deny()
		}
		clearrules = true
	}
	/* can't truncate anything, so just disregard */
	mode := t.Mode & ^(byte(plan9.OTRUNC | plan9.OCEXEC))
	/* can't execute or remove anything */
	if mode == plan9.OEXEC || (mode&plan9.ORCLOSE != 0) {
		return deny()
	}
	m := uint32(0)
	switch mode {
	default:
		return deny()
	case plan9.OREAD:
		m = 0o0400
	case plan9.OWRITE:
		m = 0o0200
	case plan9.ORDWR:
		m = 0o0600
	}
	if ((f.dir.perm & ^uint32(plan9.DMDIR|plan9.DMAPPEND)) & m) != m {
		return deny()
	}
	if f.qid.Path == Qrules && (mode == plan9.OWRITE || mode == plan9.ORDWR) {
		fsys.rulesref.Lock()
		if fsys.rulesrefcount != 0 {
			fsys.rulesref.Unlock()
			fsys.respond(t, Einuse)
			return t
		} else {
			fsys.rulesrefcount++
			fsys.rulesref.Unlock()
		}
	}
	if clearrules {
		fsys.rules.clear()
	}
	t.Qid = f.qid
	t.Iounit = 0
	fsys.queue.Lock()
	f.mode = mode
	f.open = true
	f.dir.nopen++
	f.nextopen = f.dir.fopen
	f.dir.fopen = f
	queueheld(f.dir)
	fsys.queue.Unlock()
	fsys.respond(t, "")
	return t
}

func (fsys *Fsys) create(t *plan9.Fcall, _ []byte, _ *Fid) *plan9.Fcall {
	fsys.respond(t, Eperm)
	return t
}

func (fsys *Fsys) read(t *plan9.Fcall, buf []byte, f *Fid) *plan9.Fcall {
	if f.qid.Path != Qdir {
		if f.qid.Path == Qrules {
			return fsys.readrules(t)
		}
		/* read from port */
		if f.qid.Path < NQID {
			fsys.respond(t, "internal error: unknown read port")
			return t
		}
		fsys.queue.Lock()
		defer fsys.queue.Unlock()
		queueread(f.dir, t, f)
		fsys.drainqueue(f.dir)
		return nil
	}
	// Any other read is of a directory.  Pass back all entries.
	o := int(t.Offset)
	e := o + int(t.Count)
	clock := getclock()
	d := fsys.rules.dir[1:]
	var b []byte
	var bb bytes.Buffer
	for i := 0; len(d) > 0 && i < e; i += len(b) {
		b = fsys.dostat(d[0], clock)
		if len(b) < 2 /*BIT16SZ*/ {
			break
		}
		if i >= o {
			bb.Write(b)
		}
		d = d[1:]
	}
	t.Data = bb.Bytes()
	t.Count = uint32(bb.Len())
	fsys.respond(t, "")
	return t
}

func getclock() uint32 { return uint32(time.Now().Unix()) }

func (fsys *Fsys) readrules(t *plan9.Fcall) *plan9.Fcall {
	p := fsys.rules.String()
	n := uint64(len(p))
	t.Data = []byte(p)
	if t.Offset >= uint64(n) {
		t.Count = 0
		t.Data = t.Data[0:0]
	} else {
		t.Data = []byte(p)[t.Offset:]
		if t.Offset+uint64(t.Count) > n {
			t.Count = uint32(n - t.Offset)
			t.Data = t.Data[:t.Count] // PAL: Is this necessary?
		}
	}
	fsys.respond(t, "")
	return t
}

func (fsys *Fsys) write(t *plan9.Fcall, buf []byte, f *Fid) *plan9.Fcall {
	var data []byte
	switch f.qid.Path {
	case Qdir:
		fsys.respond(t, Eisdir)
		return t
	case Qrules:
		fsys.clock = getclock()
		err := fsys.writerules(t.Data)
		t.Count = uint32(len(t.Data))
		if err != nil {
			fsys.respond(t, err.Error())
		} else {
			fsys.respond(t, "")
		}
		return t
	case Qsend:
		if f.offset == 0 {
			data = t.Data
		} else {
			// partial message already assembled
			f.writebuf = append(f.writebuf[:f.offset], t.Data[:t.Count]...) // Probably don't need the slicing counts.
			data = f.writebuf
		}
		m, n := unpackPartial(data)
		if m == nil {
			if n == 0 {
				f.offset = 0
				f.writebuf = nil
				fsys.respond(t, Ebadmsg)
				return t
			}
			if f.offset == 0 {
				f.writebuf = make([]byte, t.Count)
				copy(f.writebuf, t.Data[0:t.Count])
			}
			t.Count = uint32(len(t.Data))
			f.offset += int64(t.Count)
			fsys.respond(t, "")
			return t
		}
		/* release partial buffer */
		f.offset = 0
		f.writebuf = nil
		for _, r := range fsys.rules.rs {
			if e := matchruleset(m, r); e != nil {
				fsys.dispose(t, m, r, e)
				return nil
			}
		}
		if m.Dst != "" {
			fsys.dispose(t, m, nil, nil)
			return nil
		}
		fsys.respond(t, "no matching plumb rule")
		return t
	}
	fsys.respond(t, "internal error: write to unknown file")
	return t
}

func (fsys *Fsys) dispose(t *plan9.Fcall, m *plumb.Message, rs *Ruleset, e *Exec) {
	fsys.queue.Lock()
	var err string
	if m.Dst == "" {
		err = Enoport
		if rs != nil {
			err = startup(rs, e)
		}
	} else {
		for i := NQID; i < len(fsys.rules.dir); i++ {
			if m.Dst == fsys.rules.dir[i].name {
				if fsys.rules.dir[i].nopen == 0 {
					err = startup(rs, e)
					if e != nil && e.holdforclient {
						hold(m, fsys.rules.dir[i])
					} else {
						m = nil // release the message
					}
				} else {
					queuesend(fsys.rules.dir[i], m)
					fsys.drainqueue(fsys.rules.dir[i])
				}
				break
			}
		}
	}
	fsys.queue.Unlock()
	t.Count = uint32(len(t.Data))
	fsys.respond(t, err)
}

func hold(m *plumb.Message, d *Dirtab) {
	var q *Holdq

	h := &Holdq{}
	h.msg = m
	/* add to end of queue */
	if d.holdq == nil {
		d.holdq = h
	} else {
		for q = d.holdq; q.next != nil; q = q.next {
		}
		q.next = h
	}
}

func (fsys *Fsys) clunk(t *plan9.Fcall, _ []byte, f *Fid) *plan9.Fcall {
	fsys.queue.Lock()

	if f.open {
		d := f.dir
		d.nopen--
		if d.qid == Qrules && (f.mode == plan9.OWRITE || f.mode == plan9.ORDWR) {
			fsys.writerules(nil)
			fsys.rulesref.Lock()
			fsys.rulesrefcount--
			fsys.rulesref.Unlock()
		}
		prev := (*Fid)(nil)
		for p := d.fopen; p != nil; p = p.nextopen {
			if p == f {
				if prev != nil {
					prev.nextopen = f.nextopen
				} else {
					d.fopen = f.nextopen
				}
				removesenders(d, f)
				break
			}
			prev = p
		}
	}
	f.busy = false
	f.open = false
	f.offset = 0
	f.writebuf = nil
	fsys.queue.Unlock()
	fsys.respond(t, "")
	return t
}

func (fsys *Fsys) remove(t *plan9.Fcall, buf []byte, _ *Fid) *plan9.Fcall {
	fsys.respond(t, Eperm)
	return t
}

func (fsys *Fsys) stat(t *plan9.Fcall, buf []byte, f *Fid) *plan9.Fcall {
	t.Stat = fsys.dostat(f.dir, fsys.clock)
	fsys.respond(t, "")
	t.Stat = nil
	return t
}

func (fsys *Fsys) wstat(t *plan9.Fcall, buf []byte, _ *Fid) *plan9.Fcall {
	fsys.respond(t, Eperm)
	return t
}

func queueheld(d *Dirtab) {
	for d.holdq != nil {
		h := d.holdq
		d.holdq = h.next
		queuesend(d, h.msg)
	}
}

/* remove messages awaiting delivery to now-closing fid */
func removesenders(d *Dirtab, fid *Fid) {
	var prevs, nexts *Sendreq
	for s := d.sendq; s != nil; s = nexts {
		nexts = s.next
		for i := 0; i < s.nfid; i++ {
			if fid == s.fid[i] {
				s.fid[i] = nil
				s.nleft--
				break
			}
		}
		if s.nleft == 0 {
			s.fid = nil
			if prevs != nil {
				prevs.next = s.next
			} else {
				d.sendq = s.next
			}
		} else {
			prevs = s
		}
	}
}

func queuesend(d *Dirtab, m *plumb.Message) {
	s := &Sendreq{}
	s.nfid = d.nopen
	s.nleft = s.nfid
	s.fid = make([]*Fid, s.nfid)
	i := 0
	for f := d.fopen; f != nil; f = f.nextopen {
		s.fid[i] = f
		i++
	}
	s.msg = m
	s.next = nil
	/* link to end of queue; drainqueue() searches in sender order so this implements a FIFO */
	var t *Sendreq
	for t = d.sendq; t != nil; t = t.next {
		if t.next == nil {
			break
		}
	}
	if t == nil {
		d.sendq = s
	} else {
		t.next = s
	}
}

func queueread(d *Dirtab, t *plan9.Fcall, f *Fid) {
	r := Readreq{
		fcall: t,
		buf:   nil,
		fid:   f,
		next:  d.readq,
	}
	d.readq = &r
}

func (fsys *Fsys) drainqueue(d *Dirtab) {
	var nexts, prevs *Sendreq
	var nextr, prevr *Readreq

	for s := d.sendq; s != nil; s = nexts {
		nexts = s.next
		for i := 0; i < s.nfid; i++ {
			prevr = nil
			for r := d.readq; r != nil; r = nextr {
				nextr = r.next
				if r.fid == s.fid[i] {
					if s.pack == nil {
						s.pack = plumbpack(s.msg)
					}
					r.fcall.Data = s.pack[r.fid.offset:]
					n := uint(len(r.fcall.Data)) // len(s.pack) - r.fid.offset
					if n > fsys.messagesize-uint(plan9.IOHDRSZ) {
						n = fsys.messagesize - uint(plan9.IOHDRSZ)
					}
					if n > uint(r.fcall.Count) {
						n = uint(r.fcall.Count)
					}
					r.fcall.Count = uint32(n)
					r.fcall.Data = r.fcall.Data[:r.fcall.Count]
					fsys.respond(r.fcall, "")
					r.fid.offset += int64(n)
					if r.fid.offset >= int64(len(s.pack)) {
						/* message transferred; delete this fid from send queue */
						r.fid.offset = 0
						s.fid[i] = nil
						s.nleft--
					}
					if prevr != nil {
						prevr.next = r.next
					} else {
						d.readq = r.next
					}
					break
				} else {
					prevr = r
				}
			}
		}
		/* if no fids left, delete this send from queue */
		if s.nleft == 0 {
			if prevs != nil {
				prevs.next = s.next
			} else {
				d.sendq = s.next
			}
		} else {
			prevs = s
		}
	}
}

func packattr(attr *plumb.Attribute, w io.Writer) {
	for a := attr; a != nil; a = a.Next {
		if a != attr {
			fmt.Fprint(w, " ")
		}
		fmt.Fprintf(w, "%s=%s", a.Name, quoteAttribute(a.Value))
	}
	fmt.Fprintf(w, "\n")
}

func plumbpack(m *plumb.Message) []byte {
	p := bytes.Buffer{}
	p.WriteString(m.Src)
	p.WriteRune('\n')
	p.WriteString(m.Dst)
	p.WriteRune('\n')
	p.WriteString(m.Dir)
	p.WriteRune('\n')
	p.WriteString(m.Type)
	p.WriteRune('\n')
	packattr(m.Attr, &p)
	p.WriteString(fmt.Sprintf("%d\n", len(m.Data)))
	p.Write(m.Data)
	return p.Bytes()
}

func (fsys *Fsys) dostat(dir *Dirtab, clock uint32) []byte {
	var d plan9.Dir

	d.Qid.Type = dir.typ
	d.Qid.Path = dir.qid
	d.Qid.Vers = 0
	d.Mode = plan9.Perm(dir.perm)
	d.Length = 0 /* would be nice to do better */
	d.Name = dir.name
	d.Uid = fsys.user
	d.Gid = fsys.user
	d.Muid = fsys.user
	d.Atime = clock
	d.Mtime = clock
	b, _ := d.Bytes()
	return b
}

func unpackPartial(data []byte) (m *plumb.Message, remain int) {
	m = &plumb.Message{}
	i := 0
	var attr, ntext string
	consumed := 0
	m.Src, i = plumbline(data[consumed:])
	consumed += i
	m.Dst, i = plumbline(data[consumed:])
	consumed += i
	m.Dir, i = plumbline(data[consumed:])
	consumed += i
	m.Type, i = plumbline(data[consumed:])
	consumed += i
	attr, i = plumbline(data[consumed:])
	consumed += i
	ntext, i = plumbline(data[consumed:])
	consumed += i
	dlen, _ := strconv.Atoi(string(ntext))
	if dlen != len(data)-consumed {
		return nil, dlen - (len(data) - consumed)
	}
	m.Attr = readAttr([]byte(attr))
	m.Data = data[consumed:]
	return m, 0
}

func plumbline(data []byte) (line string, consumed int) {
	if len(data) == 0 {
		return "", 0
	}
	idx := bytes.Index(data, []byte{'\n'})
	if idx < 0 {
		return "", 0
	} else {
		return string(data[0:idx]), idx + 1
	}
}

// Lifted & adapated from internals of 9fans.net/go/plumb

var (
	ErrAttribute = errors.New("bad attribute syntax")
	ErrQuote     = errors.New("bad attribute quoting")
)

type reader struct {
	r    io.ByteReader
	buf  []byte
	attr *plumb.Attribute
	err  error
}

func newReader(r io.ByteReader) *reader {
	return &reader{
		r:   r,
		buf: make([]byte, 128),
	}
}

const quote = '\''

func readAttr(in []byte) *plumb.Attribute {
	r := newReader(bytes.NewBuffer(in))
	r.buf = r.buf[:0]
	var c byte
	quoting := false
Loop:
	for r.err == nil {
		c, r.err = r.r.ReadByte()
		if r.err == io.EOF { // differ from plumb - we pass a line in, not the whole message.
			r.err = nil
			c = '\n'
		}
		if quoting && c == quote {
			r.buf = append(r.buf, c)
			c, r.err = r.r.ReadByte()
			if c != quote {
				quoting = false
			}
		}
		if !quoting {
			switch c {
			case '\n':
				break Loop
			case quote:
				quoting = true
			case ' ':
				r.newAttr()
				r.buf = r.buf[:0]
				continue Loop // Don't add the space.
			}
		}
		r.buf = append(r.buf, c)
	}
	if len(r.buf) > 0 && r.err == nil {
		r.newAttr()
	}
	// Attributes are ordered so reverse the list.
	var next, rattr *plumb.Attribute
	for a := r.attr; a != nil; a = next {
		next = a.Next
		a.Next = rattr
		rattr = a
	}
	return rattr
}

func (r *reader) newAttr() {
	equals := bytes.IndexByte(r.buf, '=')
	if equals < 0 {
		r.err = ErrAttribute
		return
	}
	str := string(r.buf)
	r.attr = &plumb.Attribute{
		Name: str[:equals],
		Next: r.attr,
	}
	r.attr.Value, r.err = unquoteAttribute(str[equals+1:])
}

// unquoteAttribute unquotes the attribute value, if necessary, and returns the result.
func unquoteAttribute(s string) (string, error) {
	if !strings.Contains(s, "'") {
		return s, nil
	}
	if len(s) < 2 || s[0] != quote || s[len(s)-1] != quote {
		return s, ErrQuote
	}
	s = s[1 : len(s)-1]
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == quote { // Must be doubled.
			if i == len(s)-1 || s[i+1] != quote {
				return s, ErrQuote
			}
			i++
		}
		b = append(b, c)
	}
	return string(b), nil
}

// quoteAttribute quotes the attribute value, if necessary, and returns the result.
func quoteAttribute(s string) string {
	if !strings.ContainsAny(s, " '=\t") {
		return s
	}
	b := make([]byte, 0, 10+len(s)) // Room for a couple of quotes and a few backslashes.
	b = append(b, quote)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == quote {
			b = append(b, quote)
		}
		b = append(b, c)
	}
	b = append(b, quote)
	return string(b)
}
