package storage

import (
	"bufio"
	"bytes"
	"io"
	"mime"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// EMLMeta holds common headers for browser display / search.
type EMLMeta struct {
	Subject    string
	From       string
	To         string
	Cc         string
	ReceivedAt time.Time // from Date header when parseable
}

// DisplayNameFor returns a human-friendly name for browser listings.
func DisplayNameFor(absPath, fileName string) string {
	lower := strings.ToLower(fileName)
	if !strings.HasSuffix(lower, ".eml") {
		return fileName
	}
	if subj, ok := subjectFromEMLFilename(fileName); ok {
		return subj + ".eml"
	}
	meta := PeekEMLMeta(absPath)
	if meta.Subject != "" {
		return meta.Subject + ".eml"
	}
	return fileName
}

func subjectFromEMLFilename(name string) (string, bool) {
	base := strings.TrimSuffix(name, filepath.Ext(name))
	i := strings.LastIndex(base, "__")
	if i <= 0 || i+2 >= len(base) {
		return "", false
	}
	id := base[i+2:]
	if len(id) < 6 || len(id) > 16 {
		return "", false
	}
	subj := strings.ReplaceAll(base[:i], "_", " ")
	subj = strings.TrimSpace(subj)
	if subj == "" || subj == "ohne-betreff" {
		return "", false
	}
	return subj, true
}

// PeekEMLSubject is a convenience wrapper.
func PeekEMLSubject(absPath string) string {
	return PeekEMLMeta(absPath).Subject
}

// PeekEMLMeta reads Subject/From/To/Cc/Date from the EML header block.
func PeekEMLMeta(absPath string) EMLMeta {
	f, err := openRegularFile(absPath)
	if err != nil {
		return EMLMeta{}
	}
	defer f.Close()
	return PeekEMLMetaReader(f)
}

// openRegularFile opens a regular file after rejecting traversal / NUL.
// The `..` check is the sanitizer CodeQL go/path-injection looks for at this sink.
func openRegularFile(absPath string) (*os.File, error) {
	absPath, err := GuardPath(absPath)
	if err != nil {
		return nil, os.ErrInvalid
	}
	st, err := os.Lstat(absPath)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, os.ErrInvalid
	}
	return os.Open(absPath)
}

// PeekEMLMetaReader parses headers from an EML stream (stops after headers).
func PeekEMLMetaReader(r io.Reader) EMLMeta {
	br := bufio.NewReader(io.LimitReader(r, 512<<10)) // 512 KiB header cap
	if err := stripUTF8BOM(br); err != nil {
		return EMLMeta{}
	}
	// Buffer so we can fall back if net/mail rejects quirky Exchange MIME.
	buf, err := io.ReadAll(br)
	if err != nil || len(buf) == 0 {
		return EMLMeta{}
	}
	if meta, ok := peekWithMail(bytes.NewReader(buf)); ok {
		return meta
	}
	return peekManual(bytes.NewReader(buf))
}

func stripUTF8BOM(br *bufio.Reader) error {
	b, err := br.Peek(3)
	if err != nil && err != io.EOF {
		return err
	}
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		_, _ = br.Discard(3)
	}
	return nil
}

func peekWithMail(r io.Reader) (EMLMeta, bool) {
	msg, err := mail.ReadMessage(r)
	if err != nil || msg == nil {
		return EMLMeta{}, false
	}
	h := msg.Header
	meta := EMLMeta{
		Subject: cleanHeader(h.Get("Subject")),
		From:    cleanHeader(h.Get("From")),
		To:      cleanHeader(h.Get("To")),
		Cc:      cleanHeader(h.Get("Cc")),
	}
	if meta.From == "" {
		meta.From = cleanHeader(h.Get("Sender"))
	}
	if meta.To == "" {
		// Some automated mail only sets Delivered-To / Cc.
		meta.To = cleanHeader(h.Get("Delivered-To"))
	}
	if t, err := h.Date(); err == nil {
		meta.ReceivedAt = t.UTC()
	} else if raw := h.Get("Date"); raw != "" {
		meta.ReceivedAt = parseEMLDate(raw)
	}
	// Consider success if any useful header was found.
	if meta.Subject == "" && meta.From == "" && meta.To == "" && meta.ReceivedAt.IsZero() {
		return EMLMeta{}, false
	}
	return meta, true
}

func peekManual(r io.Reader) EMLMeta {
	want := map[string]*strings.Builder{
		"subject:": {},
		"from:":    {},
		"to:":      {},
		"cc:":      {},
		"date:":    {},
	}
	var current *strings.Builder
	br := bufio.NewReader(r)
	for n := 0; n < 800; n++ {
		line, err := br.ReadBytes('\n')
		if len(line) == 0 && err != nil {
			break
		}
		trim := bytes.TrimRight(line, "\r\n")
		if len(trim) == 0 {
			break
		}
		if current != nil && (trim[0] == ' ' || trim[0] == '\t') {
			current.WriteByte(' ')
			current.Write(bytes.TrimSpace(trim))
			continue
		}
		current = nil
		lower := strings.ToLower(string(trim))
		for prefix, b := range want {
			if strings.HasPrefix(lower, prefix) {
				current = b
				current.WriteString(strings.TrimSpace(string(trim[len(prefix):])))
				break
			}
		}
	}
	return EMLMeta{
		Subject:    cleanHeader(want["subject:"].String()),
		From:       cleanHeader(want["from:"].String()),
		To:         cleanHeader(want["to:"].String()),
		Cc:         cleanHeader(want["cc:"].String()),
		ReceivedAt: parseEMLDate(want["date:"].String()),
	}
}

func cleanHeader(s string) string {
	s = DecodeMIMEHeader(strings.TrimSpace(s))
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "")
	}
	// Collapse whitespace for single-line table cells.
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 180 {
		s = s[:180] + "…"
	}
	return s
}

func parseEMLDate(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	if t, err := mail.ParseDate(raw); err == nil {
		return t.UTC()
	}
	return time.Time{}
}

// DecodeMIMEHeader decodes RFC 2047 encoded-words (e.g. =?iso-8859-1?Q?...?=).
// Plain UTF-8 headers are returned unchanged.
func DecodeMIMEHeader(s string) string {
	if !strings.Contains(s, "=?") {
		return s
	}
	decoded, err := new(mime.WordDecoder).DecodeHeader(s)
	if err != nil || decoded == "" {
		return s
	}
	return decoded
}

// EnrichEMLEntry fills Subject/From/To/ReceivedAt on a BrowseEntry when the path is an .eml.
func EnrichEMLEntry(absPath, fileName string, e *BrowseEntry) {
	if e == nil || e.IsDir || !strings.HasSuffix(strings.ToLower(fileName), ".eml") {
		return
	}
	applyEMLMeta(PeekEMLMeta(absPath), fileName, e)
}

// EnrichEMLEntryReader fills metadata from an open EML stream.
func EnrichEMLEntryReader(r io.Reader, fileName string, e *BrowseEntry) {
	if e == nil || e.IsDir || !strings.HasSuffix(strings.ToLower(fileName), ".eml") {
		return
	}
	applyEMLMeta(PeekEMLMetaReader(r), fileName, e)
}

func applyEMLMeta(meta EMLMeta, fileName string, e *BrowseEntry) {
	if meta.Subject == "" {
		if subj, ok := subjectFromEMLFilename(fileName); ok {
			meta.Subject = subj
		}
	}
	if meta.Subject != "" {
		e.Subject = meta.Subject
	}
	if meta.From != "" {
		e.From = meta.From
	}
	if meta.To != "" {
		e.To = meta.To
	}
	if !meta.ReceivedAt.IsZero() {
		e.ReceivedAt = meta.ReceivedAt
	}
	if e.Subject != "" {
		e.Name = e.Subject + ".eml"
	} else if e.Name == "" || e.Name == fileName {
		e.Name = fileName
	}
}

// EMLMatchesQuery reports whether path/name or EML headers match query (lowercased).
func EMLMatchesQuery(absPath, relSlash, name, queryLower string) bool {
	if strings.Contains(strings.ToLower(relSlash), queryLower) || strings.Contains(strings.ToLower(name), queryLower) {
		return true
	}
	if !strings.HasSuffix(strings.ToLower(name), ".eml") {
		return false
	}
	if subj, ok := subjectFromEMLFilename(name); ok && strings.Contains(strings.ToLower(subj), queryLower) {
		return true
	}
	meta := PeekEMLMeta(absPath)
	fields := []string{meta.Subject, meta.From, meta.To, meta.Cc}
	for _, f := range fields {
		if f != "" && strings.Contains(strings.ToLower(f), queryLower) {
			return true
		}
	}
	return false
}
