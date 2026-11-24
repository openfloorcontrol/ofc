package frontend

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/openfloorcontrol/ofc/floor"
)

// Prompt is input given on the command line: text and image files to
// attach to it.
type Prompt struct {
	Text   string
	Images []string // file paths
}

// Empty reports whether there is nothing to send.
func (p Prompt) Empty() bool { return p.Text == "" && len(p.Images) == 0 }

// oneShot reports whether the frontend should end once the floor is back
// to the user: a prompt was given, and it isn't a command.
func (p Prompt) oneShot() bool { return !p.Empty() && !floor.IsCommand(p.Text) }

// post uploads the prompt's images and sends it.
func (p Prompt) post(client SessionClient) error {
	in := floor.UserInput{Text: p.Text}
	for _, path := range p.Images {
		att, err := attachFile(client, path)
		if err != nil {
			return err
		}
		in.Attachments = append(in.Attachments, att)
	}
	return client.PostUserInput(in)
}

// attachFile uploads the image at path to the session.
func attachFile(client SessionClient, path string) (floor.Attachment, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return floor.Attachment{}, err
	}
	return client.AttachImage(filepath.Base(path), data)
}

// attachmentLabel is how a terminal shows an attachment.
func attachmentLabel(a floor.Attachment) string {
	return fmt.Sprintf("[📎 %s, %s, %s]", a.Name, a.MIME, humanSize(a.Size))
}

func humanSize(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
