#include <u.h>
#include <libc.h>
#include <draw.h>
#include <thread.h>
#include <cursor.h>
#include <mouse.h>
#include <keyboard.h>
#include <frame.h>
#include <fcall.h>
#include <plumb.h>
#include <libsec.h>
#include <complete.h>
#include "dat.h"
#include "fns.h"
#include "riff.h"

TSLanguage *tree_sitter_c();

char cb[2048];

const char *
readfromtext(void *payload, uint bft, TSPoint position, uint *brd)
{
	Rune r;
	int n, s;
	Text *t;

	if(payload == nil){
		printf("nil");
		return nil;
	}
	t = (Text *)payload;
	n = 0;
	if(!(bft <= t->file->b.nc && bft + n <= t->file->b.nc)){
		*brd = 0;
		return nil;
	}

	do{
		r = textreadc(t, bft+n);
		s = runelen(r);
		n += s;
		memcpy(&cb[n], &r, s);
		if(n < t->ncache)
			break;
	}while(n < 2047 && r != 0);

	*brd = n;
	cb[n + 1] = 0;
	return &cb[0];
}

// select cursor node selects ast node the cursor is
// currently pointing to.
void
selectcursornode(Text *t)
{
	TSNode n;
	int start, end;

	n = ts_tree_cursor_current_node(&t->cursor);
	start = ts_node_start_byte(n);
	end = ts_node_end_byte(n);
	textsetselect(t, start-1, end-1);
}

// find cursor node will
char
findcursornode(Text *t)
{
	TSNode n;
	int n0, n1, s0, s1;

	n = ts_tree_cursor_current_node(&t->cursor);
	n0 = ts_node_start_byte(n); // begin
	n1 = ts_node_end_byte(n); // end
	s0 = t->q0; // selection begining byte
	s1 = t->q1; // selection end byte

	//	n0>main(){
	//		printf("hello, world\n");
	//		   ^       ^
	//		   s0      s1
	//	}<n1
	//	selection is within nodes bounds, check the child
	if(s0 > n0 && s1 < n1){
		if(debug)
			puts("↓");
		return ts_tree_cursor_goto_first_child(&t->cursor);
	}

	//		  n1
	//		  ∨
	//	n0>main(){
	//		printf("hello, world\n");
	//		  ^       ^
	//		  s0     s1
	//	}
	//	selection is out of nodes bounds, check the next sibling
	if(s0 > n1 && s1 > n1){
		if(debug)
			puts("→");
		return ts_tree_cursor_goto_next_sibling(&t->cursor);
	}

	//	main(){
	//			 n1
	//			 ∨
	//	n0 >printf("hello, world\n");
	//		  ^       ^
	//		  s0     s1
	//	}
	//	selection is out of nodes bounds, check the parent
	do{
		if(debug)
			puts("↑");
		if(!ts_tree_cursor_goto_parent(&t->cursor))
			return false; // we are already at the root
		n0 = ts_node_start_byte(ts_tree_cursor_current_node(&t->cursor)); // begin
		n1 = ts_node_end_byte(ts_tree_cursor_current_node(&t->cursor)); // end
		if(s0 > n0 && s1 < n1)
			return false;
	}while(!(s0 < n0) && !(s1 < n1));
	if(debug)
		puts("↖");

	return false;
}

void
setparser(Text *t, Rune r)
{
	TSInput in;
	TSNode root, n;
	int n0, n1, s0, s1;

	ts_parser_set_language(t->parser, tree_sitter_c());
	in = {
		.payload = t,
		.read = &readfromtext,
		.encoding = TSInputEncodingUTF8,
	};
	t->tree = ts_parser_parse(t->parser, t->tree, in);
	root = ts_tree_root_node(t->tree);
	t->cursor = ts_tree_cursor_new(root);
	while(findcursornode(t)){
		if(debug){
			// useful for debugging :)
			n = ts_tree_cursor_current_node(&t->cursor);
			char *string = ts_node_string(n);
			n0 = ts_node_start_byte(n); // begin
			n1 = ts_node_end_byte(n); // end
			s0 = t->q0; // selection begining byte
			s1 = t->q1; // selection end byte
			printf("select start=%d finish=%d\n", s0, s1);
			printf("node start=%d finish=%d\n", n0, n1);
			puts(string);
		}
	}
	selectcursornode(t);
	return;
}

void
selectmode(Text *t, Rune r)
{
	bool moved = false;
	switch (r){
	case 'c':
		moved = ts_tree_cursor_goto_first_child(&t->cursor);
	case 'n':
		moved = ts_tree_cursor_goto_next_sibling(&t->cursor);
	case 'p':
		moved = ts_tree_cursor_goto_parent(&t->cursor);
		break;
	case 'e':
		riffing = !riffing;
		next = &riffmode;
	}
	if(moved)
		selectcursornode(t);
}

void
riffmode(Text *t, Rune r)
{
	switch (r){
	case 'p':
		setparser(t, r);
		return;
	}
	next = &selectmode;
	riffing = !riffing;
	return;
}
