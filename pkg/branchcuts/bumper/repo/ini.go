package repo

import (
	"bufio"
	"io"
	"regexp"
)

var (
	sectionNamePrefixRegexp *regexp.Regexp = regexp.MustCompile(`\s*\[`)
	_                       io.ReadCloser  = &iniReadCloser{}
)

// iniReadCloser implements io.ReadCloser.
// It is a mere wrapper around bufio.Scanner as it reads the source
// line by line and bumps a section name whenever it finds one.
//
// The library gopkg.in/ini.v1 doesn't **easily** support section renaming
// so the purpose of this struct is to work around that.
//
// Each line is read into iniReadCloser.line that acts as a temporary buffer.
// iniReadCloser reads a line from bufio.Scanner, holds the result into iniReadCloser.line,
// eventually bumps it if it is a section name and finally copies it into a []byte
// every time a client calls iniReadCloser.Read.
type iniReadCloser struct {
	r          io.ReadCloser       // The underlying source
	s          *bufio.Scanner      // Scanner used to get the data from the source line by line
	bumpText   func(string) string // A function used to bump a section name
	line       string              // The current line being read by the scanner
	lineIdx    int                 // Copy the current line into the buffer starting from this position
	nextLine   bool                // Whether or not read a new line from the scanner
	addNewline bool                // Whether or not restore '\n' inside the current line
}

// Read the current line iniReadCloser.line into a buffer.
// It supports "splitting" the line into chunks by setting iniReadCloser.lineIdx
// if the buffer being passed to isn't enough to hold the current line.
//
// Walkthrough:
// 1.
// line:		super-duper-line
// lineIdx:		^
// buf:			[12]byte
// readLineIntoBuf(buf) -> buf == "super-duper-"
//
// 2.
// line:		super-duper-line
// lineIdx:		            ^
// buf:			[12]byte
// readLineIntoBuf(buf) -> buf == "line"
//
// 3.
// EOF
func (b *iniReadCloser) readLineIntoBuf(buf []byte) (n int, err error) {
	sublineLen := len(b.line) - b.lineIdx
	if len(buf) >= sublineLen {
		for i := 0; i < sublineLen; i++ {
			buf[i] = b.line[b.lineIdx+i]
		}
		b.nextLine = true
		b.lineIdx = 0
		return sublineLen, nil
	}
	for i := 0; i < len(buf); i++ {
		buf[i] = b.line[b.lineIdx+i]
	}
	b.nextLine = false
	b.lineIdx += len(buf)
	return len(buf), nil
}

// This is needed to determine whether to add a '\n' or not.
// As long as atEOF, as a result of the default bufio.ScanLines, is false, we can be sure the data being
// extracted ends with a '\n'.
//
// Walkthrough 1:
// 1.
// text:	a\nb
// idx:		^
// atEOF -> false
//
// 2.
// text:	a\nb
// idx:		   ^
// atEOF -> true
//
// Walkthrough 2:
// 1.
// text:	a
// idx:		^
// atEOF -> true
//
// Walkthrough 3:
// 1.
// text:	a\nb\n
// idx:		^
// atEOF -> false
//
// 2.
// text:	a\nb\n
// idx:		   ^
// atEOF -> false
//
// 3.
// text:	a\nb\n
// idx:		      ^
// atEOF -> true
func (b *iniReadCloser) scanLines(data []byte, atEOF bool) (advance int, token []byte, err error) {
	advance, token, err = bufio.ScanLines(data, atEOF)
	b.addNewline = !atEOF
	return
}

func (b *iniReadCloser) Read(p []byte) (n int, err error) {
	// iniReadCloser.line wasn't entirely read yet, so
	// keep reading it instead of requesting a new line from the scanner
	if !b.nextLine {
		return b.readLineIntoBuf(p)
	}
	if b.s.Scan() {
		b.line = b.s.Text()
		// bufio.Scanner split by "\n", therefore is needed to restore it
		// as it's likely a client is going to split by "\n" as well
		if b.addNewline {
			b.line += "\n"
		}
		if sectionNamePrefixRegexp.MatchString(b.line) {
			b.line = b.bumpText(b.line)
		}
		n, err := b.readLineIntoBuf(p)
		return n, err
	}
	if err := b.s.Err(); err != nil {
		return 0, err
	}
	return 0, io.EOF
}

func (b *iniReadCloser) Close() error {
	return b.r.Close()
}

func NewIniReadCloser(r io.ReadCloser, bumpText func(string) string) *iniReadCloser {
	s := bufio.NewScanner(r)
	iniReader := &iniReadCloser{
		r:        r,
		s:        s,
		nextLine: true,
		bumpText: bumpText,
	}
	s.Split(iniReader.scanLines)
	return iniReader
}
