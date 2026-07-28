package backup

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"net/textproto"
	"strings"
	"time"
)

// emlPart is one body or attachment part for reconstructed MIME.
type emlPart struct {
	ContentType string
	Disposition string // "inline" or "attachment" (empty = body)
	FileName    string
	ContentID   string
	Data        []byte
}

// emlMessage is a Graph-JSON reconstruction of a message for BuildEML.
type emlMessage struct {
	From        string
	To          []string
	Cc          []string
	Bcc         []string
	Subject     string
	Date        time.Time
	MessageID   string
	BodyType    string // "text" or "html"
	Body        string
	Attachments []emlPart
}

// buildEML constructs an RFC822-ish message from Graph metadata.
// Used when Exchange cannot convert the stored item via /$value.
func buildEML(m emlMessage) ([]byte, error) {
	var hdr bytes.Buffer
	writeEMLHeader(&hdr, "MIME-Version", "1.0")
	writeEMLHeader(&hdr, "X-M365Backup-Source", "graph-json-fallback")
	if m.From != "" {
		writeEMLHeader(&hdr, "From", m.From)
	}
	if len(m.To) > 0 {
		writeEMLHeader(&hdr, "To", strings.Join(m.To, ", "))
	}
	if len(m.Cc) > 0 {
		writeEMLHeader(&hdr, "Cc", strings.Join(m.Cc, ", "))
	}
	if len(m.Bcc) > 0 {
		writeEMLHeader(&hdr, "Bcc", strings.Join(m.Bcc, ", "))
	}
	if m.Subject != "" {
		writeEMLHeader(&hdr, "Subject", encodeHeaderWord(m.Subject))
	}
	if !m.Date.IsZero() {
		writeEMLHeader(&hdr, "Date", m.Date.UTC().Format(time.RFC1123Z))
	}
	if m.MessageID != "" {
		writeEMLHeader(&hdr, "Message-ID", m.MessageID)
	}

	bodyType := strings.ToLower(strings.TrimSpace(m.BodyType))
	if bodyType != "html" {
		bodyType = "text"
	}
	bodyCT := "text/" + bodyType + "; charset=UTF-8"
	bodyBytes := []byte(m.Body)

	if len(m.Attachments) == 0 {
		writeEMLHeader(&hdr, "Content-Type", bodyCT)
		writeEMLHeader(&hdr, "Content-Transfer-Encoding", "base64")
		hdr.WriteString("\r\n")
		writeBase64Lines(&hdr, bodyBytes)
		return hdr.Bytes(), nil
	}

	var parts bytes.Buffer
	mw := multipart.NewWriter(&parts)
	boundary := mw.Boundary()

	bodyHdr := textproto.MIMEHeader{}
	bodyHdr.Set("Content-Type", bodyCT)
	bodyHdr.Set("Content-Transfer-Encoding", "base64")
	pw, err := mw.CreatePart(bodyHdr)
	if err != nil {
		return nil, err
	}
	if err := writeBase64To(pw, bodyBytes); err != nil {
		return nil, err
	}

	for _, a := range m.Attachments {
		ah := textproto.MIMEHeader{}
		ct := a.ContentType
		if ct == "" {
			ct = "application/octet-stream"
		}
		if a.FileName != "" {
			if major, params, perr := mime.ParseMediaType(ct); perr == nil {
				if params == nil {
					params = map[string]string{}
				}
				params["name"] = a.FileName
				if formatted := mime.FormatMediaType(major, params); formatted != "" {
					ct = formatted
				}
			}
		}
		ah.Set("Content-Type", ct)
		ah.Set("Content-Transfer-Encoding", "base64")
		disp := a.Disposition
		if disp == "" {
			disp = "attachment"
		}
		params := map[string]string{}
		if a.FileName != "" {
			params["filename"] = a.FileName
		}
		if formatted := mime.FormatMediaType(disp, params); formatted != "" {
			ah.Set("Content-Disposition", formatted)
		} else {
			ah.Set("Content-Disposition", disp)
		}
		if a.ContentID != "" {
			cid := a.ContentID
			if !strings.HasPrefix(cid, "<") {
				cid = "<" + cid + ">"
			}
			ah.Set("Content-ID", cid)
		}
		ap, err := mw.CreatePart(ah)
		if err != nil {
			return nil, err
		}
		if err := writeBase64To(ap, a.Data); err != nil {
			return nil, err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}

	writeEMLHeader(&hdr, "Content-Type", fmt.Sprintf("multipart/mixed; boundary=%s", boundary))
	hdr.WriteString("\r\n")
	hdr.Write(parts.Bytes())
	return hdr.Bytes(), nil
}

func writeEMLHeader(b *bytes.Buffer, name, value string) {
	// Strip CR/LF from values to avoid header injection.
	value = strings.ReplaceAll(value, "\r", "")
	value = strings.ReplaceAll(value, "\n", "")
	b.WriteString(name)
	b.WriteString(": ")
	b.WriteString(value)
	b.WriteString("\r\n")
}

func encodeHeaderWord(s string) string {
	if s == "" || isASCIIPrintable(s) {
		return s
	}
	return mime.QEncoding.Encode("utf-8", s)
}

func isASCIIPrintable(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c > 0x7e {
			return false
		}
	}
	return true
}

func writeBase64Lines(b *bytes.Buffer, data []byte) {
	enc := base64.StdEncoding.EncodeToString(data)
	for len(enc) > 76 {
		b.WriteString(enc[:76])
		b.WriteString("\r\n")
		enc = enc[76:]
	}
	if enc != "" {
		b.WriteString(enc)
		b.WriteString("\r\n")
	}
}

func writeBase64To(w io.Writer, data []byte) error {
	var buf bytes.Buffer
	writeBase64Lines(&buf, data)
	_, err := w.Write(buf.Bytes())
	return err
}

func formatMailbox(name, address string) string {
	address = strings.TrimSpace(address)
	name = strings.TrimSpace(name)
	if address == "" {
		return ""
	}
	a := mail.Address{Name: name, Address: address}
	return a.String()
}
