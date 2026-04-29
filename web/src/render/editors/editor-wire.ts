// Boxland — shared editor WS client.
//
// Single WS client used by both the mapmaker and level-editor entry
// scripts. Wraps the existing `web/src/net/` NetClient + Mailbox
// shape with editor-specific:
//   * `Auth` frame with Realm.Designer + the WS ticket from the
//     server-rendered <meta> tag,
//   * `EditorJoinMapmaker` / `EditorJoinLevelEditor` envelope
//     after open,
//   * Promise-based snapshot await,
//   * typed `dispatch(opcode, payload)` for ops,
//   * subscribable `onDiff(handler)` for incoming diffs.
//
// Future: reconnect handling (the existing NetClient does
// auto-reconnect; this layer needs to re-send Join + re-fetch
// snapshot on resume). v1 scope: happy path only.

import * as flatbuffers from "flatbuffers";

import {
	NetClient, Mailbox, Realm, ClientKind,
	encodeClientMessage, Verb,
	type AuthParams, type ConnState,
} from "@net";
import { DesignerCommandPayload } from "@proto/designer-command-payload.js";
import { DesignerOpcode } from "@proto/designer-opcode.js";
import { EditorJoinPayload } from "@proto/editor-join-payload.js";
import { EditorSnapshot } from "@proto/editor-snapshot.js";
import { EditorDiff } from "@proto/editor-diff.js";

import type { EditorKind } from "./types";

export interface EditorWireOptions {
	wsURL: string;
	wsTicket: string;
	clientVersion?: string;
	onState?: (state: ConnState) => void;
}

/** Diff handlers receive the parsed FlatBuffers EditorDiff plus the
 *  raw frame bytes (in case they want to forward the frame on). */
export type DiffHandler = (diff: EditorDiff) => void;

/** The wire client manages the WS lifecycle. Use:
 *
 *    const wire = await EditorWire.connect({ wsURL, wsTicket });
 *    const snap = await wire.joinLevelEditor(levelID);
 *    wire.onDiff((d) => ...);
 *    wire.dispatch(DesignerOpcode.PlaceLevelEntity, payloadBytes);
 */
export class EditorWire {
	private readonly net: NetClient;
	private readonly mailbox: Mailbox;
	private snapshotResolver: ((s: EditorSnapshot) => void) | null = null;
	private snapshotRejecter: ((err: Error) => void) | null = null;
	private snapshotPromise: Promise<EditorSnapshot> | null = null;
	private readonly diffHandlers = new Set<DiffHandler>();
	private isJoined = false;

	private constructor(net: NetClient, mailbox: Mailbox) {
		this.net = net;
		this.mailbox = mailbox;
		// Editor frames don't fit the AOI mailbox model; we route
		// through an "extra" handler the NetClient supports. See
		// the `extra` arg in NetClient construction below.
	}

	static connect(opts: EditorWireOptions): Promise<EditorWire> {
		const mailbox = new Mailbox();
		// We need the wire instance from inside the onRawFrame
		// hook before construction completes. Use a forward
		// reference + a tiny indirection.
		let wire: EditorWire | null = null;

		const net = new NetClient(opts.wsURL, {
			mailbox,
			auth: (): AuthParams => ({
				realm: Realm.Designer,
				token: opts.wsTicket,
				clientKind: ClientKind.Web,
				clientVersion: opts.clientVersion ?? "editor",
			}),
			onRawFrame: (bytes: Uint8Array): boolean => {
				if (!wire) return false;
				return wire.handleFrame(bytes);
			},
		});

		if (opts.onState) net.onState(opts.onState);

		wire = new EditorWire(net, mailbox);

		// Wait for the connection to reach "open" before resolving
		// so callers can dispatch immediately afterward without
		// racing the auth handshake.
		return new Promise<EditorWire>((resolve, reject) => {
			const off = net.onState((s) => {
				if (s === "open") {
					off();
					resolve(wire!);
				} else if (s === "fatal" || s === "closed") {
					off();
					reject(new Error(`editor-wire: connection ${s} before open`));
				}
			});
			net.connect();
		});
	}

	/** Send the EditorJoinMapmaker + await the snapshot. */
	joinMapmaker(mapID: number): Promise<EditorSnapshot> {
		return this.join(DesignerOpcode.EditorJoinMapmaker, mapID);
	}

	/** Send the EditorJoinLevelEditor + await the snapshot. */
	joinLevelEditor(levelID: number): Promise<EditorSnapshot> {
		return this.join(DesignerOpcode.EditorJoinLevelEditor, levelID);
	}

	private join(opcode: number, targetID: number): Promise<EditorSnapshot> {
		if (this.snapshotPromise) {
			return this.snapshotPromise;
		}
		const b = new flatbuffers.Builder(64);
		const hint = b.createString("");
		EditorJoinPayload.startEditorJoinPayload(b);
		EditorJoinPayload.addTargetId(b, BigInt(targetID));
		EditorJoinPayload.addInstanceHint(b, hint);
		const root = EditorJoinPayload.endEditorJoinPayload(b);
		b.finish(root);
		this.dispatchRaw(opcode, b.asUint8Array());

		this.snapshotPromise = new Promise<EditorSnapshot>((resolve, reject) => {
			this.snapshotResolver = resolve;
			this.snapshotRejecter = reject;
			// Snapshot must arrive within 5s — slow networks
			// can extend this if needed; the editor would be
			// useless without it.
			setTimeout(() => {
				if (this.snapshotResolver) {
					this.snapshotRejecter?.(new Error("editor: snapshot timeout"));
					this.snapshotResolver = null;
					this.snapshotRejecter = null;
				}
			}, 5_000);
		});
		this.isJoined = true;
		return this.snapshotPromise;
	}

	/** Send one DesignerCommand envelope. The opcode-specific
	 *  payload bytes come from the caller (built via the relevant
	 *  FlatBuffers Payload.create*() helper). */
	dispatch(opcode: number, payloadBytes: Uint8Array): void {
		this.dispatchRaw(opcode, payloadBytes);
	}

	/** Subscribe to incoming diffs. Returns an unsubscribe fn. */
	onDiff(handler: DiffHandler): () => void {
		this.diffHandlers.add(handler);
		return () => { this.diffHandlers.delete(handler); };
	}

	/** Tear down. The NetClient closes the WS; subscribers stop
	 *  receiving diffs. Idempotent. */
	close(): void {
		this.diffHandlers.clear();
		this.snapshotPromise = null;
		this.snapshotResolver = null;
		this.snapshotRejecter = null;
		this.isJoined = false;
		this.net.disconnect();
	}

	// ---- internal -----------------------------------------------------

	private dispatchRaw(opcode: number, data: Uint8Array): void {
		const b = new flatbuffers.Builder(64 + data.length);
		const dataOff = DesignerCommandPayload.createDataVector(b, data);
		DesignerCommandPayload.startDesignerCommandPayload(b);
		DesignerCommandPayload.addOpcode(b, opcode);
		DesignerCommandPayload.addData(b, dataOff);
		const root = DesignerCommandPayload.endDesignerCommandPayload(b);
		b.finish(root);
		const inner = b.asUint8Array();
		this.net.sendRaw(encodeClientMessage(Verb.DesignerCommand, inner));
	}

	/** handleFrame is fed every server frame BEFORE the AOI
	 *  mailbox decodes it as a Diff. We sniff by file_identifier:
	 *    * "BLDS" -> EditorSnapshot or EditorDiff (both share id);
	 *      claim the frame and route internally.
	 *    * else: not for us; return false so the AOI mailbox
	 *      gets a shot.
	 *
	 *  Snapshots arrive once per join; diffs are ongoing. We
	 *  distinguish by whether we have an outstanding snapshot
	 *  promise to resolve. */
	private handleFrame(bytes: Uint8Array): boolean {
		if (bytes.length < 8) return false;
		// FlatBuffers file_identifier sits at bytes 4..8.
		const ident = String.fromCharCode(bytes[4]!, bytes[5]!, bytes[6]!, bytes[7]!);
		if (ident !== "BLDS") return false;

		const buf = new flatbuffers.ByteBuffer(bytes);

		if (this.snapshotResolver) {
			// First BLDS frame after Join is the snapshot.
			const snap = EditorSnapshot.getRootAsEditorSnapshot(buf);
			const resolve = this.snapshotResolver;
			this.snapshotResolver = null;
			this.snapshotRejecter = null;
			resolve(snap);
			return true;
		}
		// Subsequent BLDS frames are diffs.
		const diff = EditorDiff.getRootAsEditorDiff(buf);
		for (const h of this.diffHandlers) {
			try { h(diff); } catch (e) { console.warn("[editor-wire] diff handler", e); }
		}
		void this.mailbox; // mailbox kept on instance for future state queries
		void this.isJoined;
		return true;
	}
}
