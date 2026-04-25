package ws_test

import (
	flatbuffers "github.com/google/flatbuffers/go"

	"boxland/server/internal/proto"
)

// Test-only encoders for the FlatBuffers messages the gateway expects.
// These mirror what production clients build; keeping them here keeps
// the production code free of "encoder helpers" the runtime never uses.

func encodeAuth(realm proto.Realm, token string, kind proto.ClientKind, version string) []byte {
	b := flatbuffers.NewBuilder(64)
	tokenOffset := b.CreateString(token)
	versionOffset := b.CreateString(version)
	proto.ProtocolVersionStart(b)
	proto.ProtocolVersionAddMajor(b, 1)
	proto.ProtocolVersionAddMinor(b, 0)
	pvOffset := proto.ProtocolVersionEnd(b)

	proto.AuthStart(b)
	proto.AuthAddProtocolVersion(b, pvOffset)
	proto.AuthAddRealm(b, realm)
	proto.AuthAddToken(b, tokenOffset)
	proto.AuthAddClientKind(b, kind)
	proto.AuthAddClientVersion(b, versionOffset)
	root := proto.AuthEnd(b)
	b.Finish(root)
	return b.FinishedBytes()
}

func encodeClientMessage(verb proto.Verb, payload []byte) []byte {
	b := flatbuffers.NewBuilder(64)
	var payloadOffset flatbuffers.UOffsetT
	if len(payload) > 0 {
		proto.ClientMessageStartPayloadVector(b, len(payload))
		for i := len(payload) - 1; i >= 0; i-- {
			b.PrependByte(payload[i])
		}
		payloadOffset = b.EndVector(len(payload))
	}
	proto.ClientMessageStart(b)
	proto.ClientMessageAddVerb(b, verb)
	if payloadOffset != 0 {
		proto.ClientMessageAddPayload(b, payloadOffset)
	}
	root := proto.ClientMessageEnd(b)
	b.Finish(root)
	return b.FinishedBytes()
}

func encodeJoinMap(mapID uint32, hint string) []byte {
	b := flatbuffers.NewBuilder(32)
	hintOffset := b.CreateString(hint)
	proto.JoinMapPayloadStart(b)
	proto.JoinMapPayloadAddMapId(b, mapID)
	proto.JoinMapPayloadAddInstanceHint(b, hintOffset)
	root := proto.JoinMapPayloadEnd(b)
	b.Finish(root)
	return b.FinishedBytes()
}

func encodeMove(vx, vy int16) []byte {
	b := flatbuffers.NewBuilder(16)
	proto.MovePayloadStart(b)
	proto.MovePayloadAddVx(b, vx)
	proto.MovePayloadAddVy(b, vy)
	root := proto.MovePayloadEnd(b)
	b.Finish(root)
	return b.FinishedBytes()
}

func encodeDesignerCommand(opcode proto.DesignerOpcode) []byte {
	b := flatbuffers.NewBuilder(16)
	proto.DesignerCommandPayloadStart(b)
	proto.DesignerCommandPayloadAddOpcode(b, opcode)
	root := proto.DesignerCommandPayloadEnd(b)
	b.Finish(root)
	return b.FinishedBytes()
}
