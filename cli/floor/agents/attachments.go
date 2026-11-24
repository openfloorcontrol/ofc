package agents

import (
	"fmt"

	"github.com/openfloorcontrol/ofc/floor"
)

// imageNote is the text an agent gets in place of an image it can't see.
func imageNote(a floor.Attachment) string {
	return fmt.Sprintf("[image %s (%s) attached — not shown, this agent's model does not take images]", a.Name, a.MIME)
}

// missingImageNote is the text an agent gets for an image whose bytes
// could not be loaded.
func missingImageNote(a floor.Attachment, err error) string {
	return fmt.Sprintf("[image %s (%s) attached — could not be loaded: %v]", a.Name, a.MIME, err)
}
