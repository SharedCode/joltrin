package encoding

import (
	"bytes"
	"encoding/binary"

	"github.com/google/uuid"

	"github.com/sharedcode/joltrin/v5"
)

// HandleEncoder encodes and decodes sop.Handle values to and from compact byte representations.
type HandleEncoder struct{}

// NewHandleMarshaler returns a new HandleEncoder.
func NewHandleMarshaler() *HandleEncoder {
	return &HandleEncoder{}
}

// Marshal encodes a Handle into the provided buffer and returns the resulting bytes.
func (he HandleEncoder) Marshal(v sop.Handle, buffer []byte) ([]byte, error) {
	w := bytes.NewBuffer(buffer)
	pv := v
	encode(w, &pv)
	return w.Bytes(), nil
}

// Unmarshal decodes data into the target Handle.
func (he HandleEncoder) Unmarshal(data []byte, target *sop.Handle) error {
	r := bytes.NewBuffer(data)
	err := decode(r, target)
	return err
}

// UnmarshalLogicalID decodes only the logical UUID from an encoded Handle buffer.
func (he HandleEncoder) UnmarshalLogicalID(data []byte) (sop.UUID, error) {
	r := bytes.NewBuffer(data)
	h, err := uuid.FromBytes(r.Next(16))
	if err != nil {
		return sop.NilUUID, err
	}
	return sop.UUID(h), nil
}

func encode(w *bytes.Buffer, h *sop.Handle) (int, error) {
	w.Write(h.LogicalID[:])
	w.Write(h.PhysicalIDA[:])
	w.Write(h.PhysicalIDB[:])
	var b byte
	if h.IsActiveIDB {
		b = 1
	}
	w.Write([]byte{b})

	var dummy4 [4]byte
	binary.LittleEndian.PutUint32(dummy4[:], uint32(h.Version))
	w.Write(dummy4[:])

	var dummy8 [8]byte
	binary.LittleEndian.PutUint64(dummy8[:], uint64(h.WorkInProgressTimestamp))
	w.Write(dummy8[:])

	b = 0
	if h.IsDeleted {
		b = 1
	}
	w.Write([]byte{b})

	return w.Len(), nil
}

func decode(r *bytes.Buffer, target *sop.Handle) error {
	h, err := uuid.FromBytes(r.Next(16))
	if err != nil {
		return err
	}
	target.LogicalID = sop.UUID(h)

	h, err = uuid.FromBytes(r.Next(16))
	if err != nil {
		return err
	}
	target.PhysicalIDA = sop.UUID(h)

	h, err = uuid.FromBytes(r.Next(16))
	if err != nil {
		return err
	}
	target.PhysicalIDB = sop.UUID(h)

	var b byte = r.Next(1)[0]
	if b == 1 {
		target.IsActiveIDB = true
	}

	target.Version = int32(binary.LittleEndian.Uint32(r.Next(4)))
	target.WorkInProgressTimestamp = int64(binary.LittleEndian.Uint64(r.Next(8)))

	b = r.Next(1)[0]
	if b == 1 {
		target.IsDeleted = true
	}

	return nil
}
