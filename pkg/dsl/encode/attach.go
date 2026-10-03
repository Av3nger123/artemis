package encode

import "artemis/pkg/dsl/token"

// Reattaching comments is the mirror of comments.go's extraction, and the three
// helpers here are named for the three shapes a node has: no braces of its own
// (plain), its own braces (braced), and braces with no head (braces, which is a
// block).
//
// Every slot lands on the token extraction would read it from again:
//
//	above, blankAbove  the node's first token's Leading
//	after              the node's first token's Trailing
//	open               the opening brace's Trailing
//	beforeClose        the closing brace's Leading
//	afterClose         the closing brace's Trailing
//
// `after` goes on the *first* token rather than the last, which is where
// extraction found it. That is deliberate and it round-trips: endOfLine scans a
// node's tokens in order and takes every trailing comment it finds, so a comment
// moved from the end of the node to the front of it comes back in the same
// position in the same list -- and canonical layout puts every one of them at
// the end of the line regardless.

// plain attaches the statement slots of a node with no braces of its own.
func plain(f fields, first *token.Token) error {
	c, present, err := commentsOf(f)
	if err != nil || !present {
		return err
	}
	return c.statement(first)
}

// braced attaches all six slots of a node that owns a brace pair.
func braced(f fields, first, lbrace, rbrace *token.Token) error {
	c, present, err := commentsOf(f)
	if err != nil || !present {
		return err
	}
	if err := c.statement(first); err != nil {
		return err
	}
	return c.braces(lbrace, rbrace)
}

// braces attaches the three brace slots of a block, which has no head of its
// own: its opening brace sits at the end of the line its parent renders, so the
// statement slots there belong to the parent.
func braces(f fields, lbrace, rbrace *token.Token) error {
	c, present, err := commentsOf(f)
	if err != nil || !present {
		return err
	}
	return c.braces(lbrace, rbrace)
}

// slots is one node's comment group, read.
type slots struct {
	above       []comment
	blankAbove  bool
	after       []string
	open        []string
	beforeClose []comment
	afterClose  []string
}

func (c slots) statement(first *token.Token) error {
	if first == nil {
		return nil
	}
	attachAbove(first, c.above, c.blankAbove)
	attachAfter(first, c.after)
	return nil
}

func (c slots) braces(lbrace, rbrace *token.Token) error {
	if lbrace != nil {
		attachAfter(lbrace, c.open)
	}
	if rbrace != nil {
		attachAbove(rbrace, c.beforeClose, false)
		attachAfter(rbrace, c.afterClose)
	}
	return nil
}

// commentsOf reads a node's comment group. Absent is the common case -- most
// nodes carry none -- so it is not an error.
func commentsOf(f fields) (slots, bool, error) {
	o, present, err := f.child("comments")
	if err != nil || !present {
		return slots{}, false, err
	}
	var c slots
	if c.above, err = commentList(o, "above"); err != nil {
		return c, true, err
	}
	if c.blankAbove, err = o.flag("blankAbove"); err != nil {
		return c, true, err
	}
	if c.after, err = o.strList("after"); err != nil {
		return c, true, err
	}
	if c.open, err = o.strList("open"); err != nil {
		return c, true, err
	}
	if c.beforeClose, err = commentList(o, "beforeClose"); err != nil {
		return c, true, err
	}
	if c.afterClose, err = o.strList("afterClose"); err != nil {
		return c, true, err
	}
	return c, true, nil
}

// commentList reads one of the two own-line slots, whose entries carry a blank
// flag as well as their text.
func commentList(f fields, key string) ([]comment, error) {
	items, err := f.list(key)
	if err != nil {
		return nil, err
	}
	out := make([]comment, 0, len(items))
	for _, it := range items {
		o, err := it.object()
		if err != nil {
			return nil, err
		}
		text, err := o.str("text")
		if err != nil {
			return nil, err
		}
		blank, err := o.flag("blank")
		if err != nil {
			return nil, err
		}
		out = append(out, comment{Text: text, Blank: blank})
	}
	return out, nil
}
