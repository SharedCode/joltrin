package encoding

import (
	"testing"

	"github.com/google/uuid"
	"github.com/sharedcode/joltrin/v5"
)

func TestUUIDMarshalling(t *testing.T) {
	id := sop.NewUUID()
	ba := id[:]

	id2, _ := uuid.FromBytes(ba)
	if id != sop.UUID(id2) {
		t.Fail()
	}
}

func TestHandleMarshalling(t *testing.T) {
	h := sop.NewHandle(sop.NewUUID())
	h.PhysicalIDA = sop.NewUUID()
	h.PhysicalIDB = sop.NewUUID()
	h.IsActiveIDB = true
	h.Version = 29
	h.WorkInProgressTimestamp = sop.Now().Unix()
	m := NewHandleMarshaler()
	buf := make([]byte, 0, sop.HandleSizeInBytes)
	ba, err := m.Marshal(h, buf)
	if err != nil {
		t.Error(err)
	}
	var targetH sop.Handle
	m.Unmarshal(ba, &targetH)
	if h != targetH {
		t.Errorf("Marshalled Handle %v did not match unmarshalled Handle %v", h, targetH)
	}
}
