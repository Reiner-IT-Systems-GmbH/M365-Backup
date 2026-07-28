package backup

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/microsoftgraph/msgraph-sdk-go/models"
	"github.com/microsoftgraph/msgraph-sdk-go/users"

	"github.com/rhw/m365backup/internal/graph"
)

// isMimeConversionFailed reports Exchange server-side MIME conversion failures
// (ErrorMimeContentConversionFailed on GET …/messages/{id}/$value).
func isMimeConversionFailed(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "errormimecontentconversionfailed") ||
		strings.Contains(s, "mime content conversion failed")
}

// fetchMessageMIMEFallback rebuilds an EML from Graph JSON + file attachments
// when /$value cannot convert the stored MAPI item.
func fetchMessageMIMEFallback(ctx context.Context, gc *graph.Client, userID, messageID string) ([]byte, error) {
	cfg := &users.ItemMessagesMessageItemRequestBuilderGetRequestConfiguration{
		QueryParameters: &users.ItemMessagesMessageItemRequestBuilderGetQueryParameters{
			Select: []string{
				"id", "subject", "from", "sender", "toRecipients", "ccRecipients", "bccRecipients",
				"receivedDateTime", "sentDateTime", "internetMessageId", "body", "hasAttachments",
			},
		},
	}
	msg, err := gc.Graph.Users().ByUserId(userID).Messages().ByMessageId(messageID).Get(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("graph message: %w", err)
	}

	em := emlMessage{
		Subject:   ptrStr(msg.GetSubject()),
		MessageID: ptrStr(msg.GetInternetMessageId()),
		From:      recipientString(msg.GetFrom()),
		To:        recipientsStrings(msg.GetToRecipients()),
		Cc:        recipientsStrings(msg.GetCcRecipients()),
		Bcc:       recipientsStrings(msg.GetBccRecipients()),
		BodyType:  "text",
	}
	if em.From == "" {
		em.From = recipientString(msg.GetSender())
	}
	if t := msg.GetReceivedDateTime(); t != nil {
		em.Date = *t
	} else if t := msg.GetSentDateTime(); t != nil {
		em.Date = *t
	}
	if body := msg.GetBody(); body != nil {
		em.Body = ptrStr(body.GetContent())
		if ct := body.GetContentType(); ct != nil && *ct == models.HTML_BODYTYPE {
			em.BodyType = "html"
		}
	}
	if em.Date.IsZero() {
		em.Date = time.Now().UTC()
	}

	atts, err := listMessageAttachments(ctx, gc, userID, messageID)
	if err != nil {
		return nil, fmt.Errorf("graph attachments: %w", err)
	}
	for _, a := range atts {
		part, perr := attachmentToPart(ctx, gc, userID, messageID, a)
		if perr != nil {
			// Keep going; note missing binary as a tiny text attachment.
			name := ptrStr(a.GetName())
			if name == "" {
				name = "attachment"
			}
			part = emlPart{
				ContentType: "text/plain; charset=UTF-8",
				Disposition: "attachment",
				FileName:    name + ".unavailable.txt",
				Data:        []byte("attachment unavailable: " + perr.Error() + "\r\n"),
			}
		}
		if part.Data == nil && part.FileName == "" {
			continue
		}
		em.Attachments = append(em.Attachments, part)
	}

	return buildEML(em)
}

func listMessageAttachments(ctx context.Context, gc *graph.Client, userID, messageID string) ([]models.Attachmentable, error) {
	top := int32(50)
	cfg := &users.ItemMessagesItemAttachmentsRequestBuilderGetRequestConfiguration{
		QueryParameters: &users.ItemMessagesItemAttachmentsRequestBuilderGetQueryParameters{
			Top: &top,
		},
	}
	resp, err := gc.Graph.Users().ByUserId(userID).Messages().ByMessageId(messageID).Attachments().Get(ctx, cfg)
	if err != nil {
		return nil, err
	}
	var all []models.Attachmentable
	for {
		all = append(all, resp.GetValue()...)
		next := resp.GetOdataNextLink()
		if next == nil || *next == "" {
			break
		}
		resp, err = gc.Graph.Users().ByUserId(userID).Messages().ByMessageId(messageID).Attachments().WithUrl(*next).Get(ctx, nil)
		if err != nil {
			return all, err
		}
	}
	return all, nil
}

func attachmentToPart(ctx context.Context, gc *graph.Client, userID, messageID string, a models.Attachmentable) (emlPart, error) {
	name := ptrStr(a.GetName())
	ct := ptrStr(a.GetContentType())
	inline := a.GetIsInline() != nil && *a.GetIsInline()
	disp := "attachment"
	if inline {
		disp = "inline"
	}

	fa, ok := a.(models.FileAttachmentable)
	if !ok {
		// itemAttachment / referenceAttachment — no raw bytes via this path.
		note := fmt.Sprintf("[%s attachment %q not included in JSON fallback]\r\n", attachmentODataType(a), name)
		return emlPart{
			ContentType: "text/plain; charset=UTF-8",
			Disposition: "attachment",
			FileName:    name + ".skipped.txt",
			Data:        []byte(note),
		}, nil
	}

	data := fa.GetContentBytes()
	if len(data) == 0 {
		aid := ptrStr(a.GetId())
		if aid == "" {
			return emlPart{}, fmt.Errorf("empty content and no attachment id")
		}
		rawURL := fmt.Sprintf("https://graph.microsoft.com/v1.0/users/%s/messages/%s/attachments/%s/$value",
			userID, messageID, aid)
		var err error
		data, err = gc.GetBytes(ctx, rawURL)
		if err != nil {
			return emlPart{}, err
		}
	}

	part := emlPart{
		ContentType: ct,
		Disposition: disp,
		FileName:    name,
		Data:        data,
	}
	if cid := ptrStr(fa.GetContentId()); cid != "" {
		part.ContentID = cid
	}
	return part, nil
}

func attachmentODataType(a models.Attachmentable) string {
	if a == nil {
		return "unknown"
	}
	if t := a.GetOdataType(); t != nil && *t != "" {
		return strings.TrimPrefix(*t, "#microsoft.graph.")
	}
	return "non-file"
}

func recipientString(r models.Recipientable) string {
	if r == nil {
		return ""
	}
	ea := r.GetEmailAddress()
	if ea == nil {
		return ""
	}
	return formatMailbox(ptrStr(ea.GetName()), ptrStr(ea.GetAddress()))
}

func recipientsStrings(rs []models.Recipientable) []string {
	var out []string
	for _, r := range rs {
		if s := recipientString(r); s != "" {
			out = append(out, s)
		}
	}
	return out
}
