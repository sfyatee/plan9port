#include <u.h>
#include <libc.h>
#include <bio.h>
#include <libString.h>
#include <fcall.h>

typedef struct Seen Seen;
struct Seen {
	Seen	*up;
	Dir	*d;
};

int Cflag = 0;
int uflag = 0;
String *stfmt;

/* should turn these flags into a mask */
int printdirs = 1;
int printfiles = 1;
int temponly = 0;
int execonly = 0;
long maxdepth = ~(1<<31);
long mindepth = 0;

char *dotpath = ".";
Dir *dotdir = nil;

Biobuf *bout;

void
warn(char *fmt, ...)
{
	va_list arg;
	char buf[1024];	/* arbitrary */
	int n;

	if((n = snprint(buf, sizeof(buf), "%s: ", argv0)) < 0)
		sysfatal("snprint: %r");
	va_start(arg, fmt);
	vseprint(buf+n, buf+sizeof(buf), fmt, arg);
	va_end(arg);

	Bflush(bout);
	fprint(2, "%s\n", buf);
}

int
seen(Seen *s, Dir *d)
{
	for(; s != nil; s = s->up)
		if(d->qid.path == s->d->qid.path
		&& d->qid.type == s->d->qid.type
		&& d->dev == s->d->dev)
			return 1;
	return 0;
}

void
dofile(char *path, Dir *f, int pathonly)
{
	char *p;

	if(
		(f == dotdir)
		|| (temponly && ! (f->qid.type & QTTMP))
		|| (execonly && ! (f->mode & DMEXEC))
	)
		return;

	for(p = s_to_c(stfmt); *p != '\0'; p++){
		switch(*p){
		case 'U': Bwrite(bout, f->uid, strlen(f->uid)); break;
		case 'G': Bwrite(bout, f->gid, strlen(f->gid)); break;
		case 'M': Bwrite(bout, f->muid, strlen(f->muid)); break;
		case 'a': Bprint(bout, "%uld", f->atime); break;
		case 'm': Bprint(bout, "%uld", f->mtime); break;
		case 'n': Bwrite(bout, f->name, strlen(f->name)); break;
		case 'p':
			if(path != dotpath)
				Bwrite(bout, path, strlen(path));
			if(! (f->qid.type & QTDIR) && !pathonly){
				if(path != dotpath)
					Bputc(bout, '/');
				Bwrite(bout, f->name, strlen(f->name));
			}
			break;
		case 'q': Bprint(bout, "%ullx.%uld.%.2uhhx", f->qid.path, f->qid.vers, f->qid.type); break;
		case 's': Bprint(bout, "%lld", f->length); break;
		case 'x': Bprint(bout, "%M", f->mode); break;

		/* These two  are slightly different, as they tell us about the fileserver instead of the file */
		case 'D': Bprint(bout, "%ud", f->dev); break;
		case 'T': Bprint(bout, "%C", f->type); break;
		default:
			abort();
		}

		if(*(p+1) != '\0')
			Bputc(bout, ' ');
	}

	Bputc(bout, '\n');

	if(uflag)
		Bflush(bout);
}

void
walk(char *path, Dir *cf, Seen *up, long depth)
{
	String *file;
	Dir *dirs, *f, *fe;
	Seen s;
	long n, fseen;
	int fd;

	if(cf == nil){
		warn("path: %s: %r", path);
		return;
	}

	fseen = seen(up, cf);
	if(depth >= maxdepth || fseen)
		goto nodescend;

	if((fd = open(path, OREAD)) < 0){
		warn("couldn't open %s: %r", path);
		return;
	}

	s.d = cf;
	s.up = up;
	while((n = dirread(fd, &dirs)) > 0){
		fe = dirs+n;
		for(f = dirs; f < fe; f++){
			if(!(f->qid.type & QTDIR)){
				if(printfiles && depth >= mindepth)
					dofile(path, f, 0);
			}else if(strcmp(f->name, ".") == 0 || strcmp(f->name, "..") == 0){
				warn(". or .. named file: %s/%s", path, f->name);
			}else{
				if(depth+1 > maxdepth){
					dofile(path, f, 0);
					continue;
				}else if(path == dotpath){
					if((file = s_new()) == nil)
						sysfatal("s_new: %r");
				}else{
					if((file = s_copy(path)) == nil)
						sysfatal("s_copy: %r");
					if(s_len(file) != 1 || *s_to_c(file) != '/')
						s_putc(file, '/');
				}
				s_append(file, f->name);
				walk(s_to_c(file), f, &s, depth+1);
				s_free(file);
			}
		}
		free(dirs);
	}
	close(fd);
	if(n < 0)
		warn("%s: %r", path);

nodescend:
	depth--;
	if(printdirs && (depth >= mindepth || fseen))
		dofile(path, cf, 0);
}

char*
slashslash(char *s)
{
	char *p, *q;

	for(p=q=s; *q; q++){
		if(q>s && *q=='/' && *(q-1)=='/')
			continue;
		if(p != q)
			*p = *q;
		p++;
	}
	do{
		*p-- = '\0';
	} while(p>s && *p=='/');

	return s;
}

long
estrtol(char *as, char **aas, int base)
{
	long n;
	char *p;

	n = strtol(as, &p, base);
	if(p == as || *p != '\0')
		sysfatal("estrtol: bad input '%s'", as);
	else if(aas != nil)
		*aas = p;

	return n;
}

void
elimdepth(char *p){
	char *q;

	if(strlen(p) == 0)
		sysfatal("empty depth argument");

	if(q = strchr(p, ',')){
		*q = '\0';
		if(p != q)
			mindepth = estrtol(p, nil, 0);
		p = q+1;
		if(*p == '\0')
			return;
	}

	maxdepth = estrtol(p, nil, 0);
}

void
usage(void)
{
	fprint(2, "usage: %s [-udftx] [-n mind,maxd] [-e statfmt] [file ...]\n", argv0);
	exits("usage");
}

/*
	Last I checked (commit 3dd6a31881535615389c24ab9a139af2798c462c),
	libString calls sysfatal when things go wrong; in my local
	copy of libString, failed calls return nil and errstr is set.

	There are various nil checks in this code when calling libString
	functions, but since they are a no-op and libString needs
	a rework, I left them in - BurnZeZ
*/
void
main(int argc, char **argv)
{
	long i;
	Dir *d;

	stfmt = nil;
	ARGBEGIN{
	case 'C': Cflag++; break; /* undocumented; do not cleanname() the args */
	case 'u': uflag++; break; /* unbuffered output */

	case 'd': printfiles = 0;	break; /* only dirs */
	case 'f': printdirs = 0;	break; /* only non-dirs */
	case 't': temponly = 1;		break; /* only tmp files */
	case 'x': execonly = 1;		break; /* only executable permission */

	case 'n': elimdepth(EARGF(usage())); break;
	case 'e':
		if((stfmt = s_reset(stfmt)) == nil)
			sysfatal("s_reset: %r");
		s_append(stfmt, EARGF(usage()));
		i = strspn(s_to_c(stfmt), "UGMamnpqsxDT");
		if(i != s_len(stfmt))
			sysfatal("bad stfmt: %s", s_to_c(stfmt));
		break;
	default:
		usage();
	}ARGEND;

	if(!printfiles && !printdirs)
		sysfatal("mutually exclusive flags: -f and -d");

	fmtinstall('M', dirmodefmt);

	if((bout = Bfdopen(1, OWRITE)) == nil)
		sysfatal("Bfdopen: %r");
	Blethal(bout, nil);
	if(stfmt == nil){
		if((stfmt = s_new()) == nil)
			sysfatal("s_new: %r");
		s_putc(stfmt, 'p');
		s_terminate(stfmt);
	}
	if(maxdepth != ~(1<<31))
		maxdepth++;
	if(argc == 0){
		dotdir = dirstat(".");
		walk(dotpath, dotdir, nil, 1);
	}else for(i=0; i<argc; i++){
		if(strncmp(argv[i], "#/", 2) == 0)
			slashslash(argv[i]+2);
		else{
			if(!Cflag)
				cleanname(argv[i]);
			slashslash(argv[i]);
		}
		if((d = dirstat(argv[i])) == nil)
			continue;
		if(d->qid.type & QTDIR){
			walk(argv[i], d, nil, 1);
		}else{
			if(printfiles && mindepth < 1)
				dofile(argv[i], d, 1);
		}
		free(d);
	}
	Bterm(bout);

	exits(nil);
}
