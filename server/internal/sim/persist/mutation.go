package persist

import (
	"errors"

	flatbuffers "github.com/google/flatbuffers/go"

	"boxland/server/internal/proto"
)

// Mutation is the in-memory shape of one delta. The encoder turns this
// into a FlatBuffers blob the WAL stores; the decoder rebuilds it on
// recovery. Keeping Go and FB shapes parallel (rather than directly
// passing the FB type around) makes hot-path code clearer to read.
type Mutation struct {
	Tick     uint64
	Seq      uint32
	Kind     MutationKind
	EntityID uint64
	TypeID   uint32
	X        int32
	Y        int32
	AuxU32   uint32
	AuxU32B  uint32
	Payload  []byte
}

// MutationKind mirrors the FlatBuffers enum so callers don't have to
// import the proto package.
type MutationKind = proto.MutationKind

const (
	MutationEntitySpawn      = proto.MutationKindEntitySpawn
	MutationEntityDespawn    = proto.MutationKindEntityDespawn
	MutationEntityMove       = proto.MutationKindEntityMove
	MutationEntitySetAnim    = proto.MutationKindEntitySetAnim
	MutationEntitySetVariant = proto.MutationKindEntitySetVariant
	MutationEntitySetTint    = proto.MutationKindEntitySetTint
	MutationTilePlace        = proto.MutationKindTilePlace
	MutationTileRemove       = proto.MutationKindTileRemove
	MutationLightingSet      = proto.MutationKindLightingSet
	MutationResourceAdjust   = proto.MutationKindResourceAdjust
)

// EncodeMutation serializes one Mutation into a self-contained FlatBuffer.
func EncodeMutation(m Mutation) ([]byte, error) {
	b := flatbuffers.NewBuilder(64)

	var payloadOffset flatbuffers.UOffsetT
	if len(m.Payload) > 0 {
		proto.MutationStartPayloadVector(b, len(m.Payload))
		for i := len(m.Payload) - 1; i >= 0; i-- {
			b.PrependByte(m.Payload[i])
		}
		payloadOffset = b.EndVector(len(m.Payload))
	}

	proto.ProtocolVersionStart(b)
	proto.ProtocolVersionAddMajor(b, ProtocolMajor)
	proto.ProtocolVersionAddMinor(b, ProtocolMinor)
	pvOffset := proto.ProtocolVersionEnd(b)

	proto.MutationStart(b)
	proto.MutationAddProtocolVersion(b, pvOffset)
	proto.MutationAddTick(b, m.Tick)
	proto.MutationAddSeq(b, m.Seq)
	proto.MutationAddKind(b, m.Kind)
	proto.MutationAddEntityId(b, m.EntityID)
	proto.MutationAddTypeId(b, m.TypeID)
	proto.MutationAddX(b, m.X)
	proto.MutationAddY(b, m.Y)
	proto.MutationAddAuxU32(b, m.AuxU32)
	proto.MutationAddAuxU32B(b, m.AuxU32B)
	if payloadOffset != 0 {
		proto.MutationAddPayload(b, payloadOffset)
	}
	root := proto.MutationEnd(b)

	proto.FinishMutationBuffer(b, root)
	return b.FinishedBytes(), nil
}

// DecodeMutation rebuilds a Mutation from the WAL blob.
func DecodeMutation(blob []byte) (Mutation, error) {
	if len(blob) < 8 {
		return Mutation{}, errors.New("persist: mutation blob too short")
	}
	mut := proto.GetRootAsMutation(blob, 0)

	pv := mut.ProtocolVersion(nil)
	if pv == nil || pv.Major() != ProtocolMajor {
		return Mutation{}, errors.New("persist: mutation protocol version mismatch")
	}
	out := Mutation{
		Tick:     mut.Tick(),
		Seq:      mut.Seq(),
		Kind:     mut.Kind(),
		EntityID: mut.EntityId(),
		TypeID:   mut.TypeId(),
		X:        mut.X(),
		Y:        mut.Y(),
		AuxU32:   mut.AuxU32(),
		AuxU32B:  mut.AuxU32B(),
	}
	if n := mut.PayloadLength(); n > 0 {
		out.Payload = mut.PayloadBytes()
	}
	return out, nil
}
