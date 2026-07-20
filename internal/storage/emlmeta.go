package storage

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// EMLMeta holds common headers for browser display / search.
type EMLMeta struct {
	Subject string
	From    string
	To      string
	Cc      string
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

// PeekEMLMeta reads Subject/From/To/Cc from the EML header block (one pass).
func PeekEMLMeta(absPath string) EMLMeta {
	f, err := os.Open(absPath)
	if err != nil {
		return EMLMeta{}
	}
	defer f.Close()

	want := map[string]*strings.Builder{
		"subject:": {},
		"from:":    {},
		"to:":      {},
		"cc:":      {},
	}
	var current *strings.Builder
	r := bufio.NewReader(f)
	for n := 0; n < 400; n++ {
		line, err := r.ReadBytes('\n')
		if len(line) == 0 && err != nil {
			break
		}
		trim := bytes.TrimRight(line, "\r\n")
		if len(trim) == 0 {
			break // end of headers
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

	clean := func(s string) string {
		s = decodeSimpleMIMEWords(strings.TrimSpace(s))
		if !utf8.ValidString(s) {
			s = strings.ToValidUTF8(s, "")
		}
		if len(s) > 180 {
			s = s[:180] + "…"
		}
		return s
	}
	return EMLMeta{
		Subject: clean(want["subject:"].String()),
		From:    clean(want["from:"].String()),
		To:      clean(want["to:"].String()),
		Cc:      clean(want["cc:"].String()),
	}
}

func decodeSimpleMIMEWords(s string) string {
	if !strings.Contains(s, "=?") {
		return s
	}
	return s
}

// EnrichEMLEntry fills Subject/From/To on a BrowseEntry when the path is an .eml.
func EnrichEMLEntry(absPath, fileName string, e *BrowseEntry) {
	if e.IsDir || !strings.HasSuffix(strings.ToLower(fileName), ".eml") {
		return
	}
	meta := PeekEMLMeta(absPath)
	if subj, ok := subjectFromEMLFilename(fileName); ok && meta.Subject == "" {
		meta.Subject = subj
	}
	e.Subject = meta.Subject
	e.From = meta.From
	e.To = meta.To
	if e.Subject != "" {
		e.Name = e.Subject + ".eml"
	} else {
		e.Name = DisplayNameFor(absPath, fileName)
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
