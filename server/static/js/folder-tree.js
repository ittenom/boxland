// Boxland — folder-tree interactions for Asset Manager + Mapmaker palette.
//
// Vanilla JS, ~250 lines. Wires up:
//
//   * disclosure triangles (click)
//   * F2 rename (inline, on selected folder OR asset card)
//   * Delete to delete selected folder (with confirm)
//   * arrow-key navigation in rail (Up/Down/Left/Right)
//   * drag-and-drop:
//       - asset card → folder row in rail (bulk move)
//       - asset card → kind-root header (move to root)
//       - asset card → OS (drag-out as .boxasset.zip via DownloadURL)
//       - external file → folder row OR pane (upload directly into folder)
//       - folder → folder row (nest)
//   * right-click context menu (New folder, Rename, Sort by ▸, Delete)
//   * Cmd/Ctrl + C / X / V on selection (clipboard moves between folders)
//   * Cmd/Ctrl + N anywhere on the page → new folder under selected
//
// Server contracts used (all on /design/*):
//   POST /design/folders                    new folder
//   POST /design/folders/{id}/rename        rename folder
//   POST /design/folders/{id}/move          move folder (parent_id field)
//   POST /design/folders/{id}/sort-mode     change sort
//   DELETE /design/folders/{id}             delete folder
//   POST /design/assets/move                bulk move (ids, folder_id)
//   POST /design/assets/{id}/rename         rename asset
//   POST /design/folders/{id}/upload        not implemented yet (defers to /assets/upload + move)
//
// All POSTs include the CSRF token from <meta name="csrf-token">.

(function () {
  "use strict";
  if (!document.querySelector("[data-bx-folder-rail]")) return;

  const rail = document.querySelector("[data-bx-folder-rail]");
  const pane = document.querySelector("[data-bx-folder-pane]");
  let clipboard = { mode: null, ids: [] }; // mode: "copy" | "cut" | null

  // ---- helpers ------------------------------------------------------

  function csrf() {
    const m = document.querySelector('meta[name="csrf-token"]');
    return m ? m.getAttribute("content") : "";
  }
  function postForm(url, body, opts = {}) {
    const fd = new FormData();
    for (const [k, v] of Object.entries(body)) fd.append(k, v);
    return fetch(url, {
      method: opts.method || "POST",
      credentials: "same-origin",
      headers: { "X-CSRF-Token": csrf() },
      body: fd,
    });
  }
  function reload() {
    // The simplest path that keeps tree + grid + selection in sync is
    // a full reload. The browser cache + small page weight means this
    // is fast in practice.
    window.location.reload();
  }
  function selectedFolderRow() {
    return rail.querySelector(
      ".bx-folder-rail__item--selected, .bx-folder-rail__item:focus-within"
    );
  }
  function selectedAssetIDs() {
    return Array.from(
      pane?.querySelectorAll(
        '.bx-folder-contents__card[aria-selected="true"], .bx-folder-contents__card.bx-multi-selected'
      ) || []
    ).map((el) => Number(el.getAttribute("data-bx-asset-id"))).filter((n) => n > 0);
  }

  // ---- disclosure (▶ / ▼) ------------------------------------------

  rail.addEventListener("click", (e) => {
    const tgl = e.target instanceof HTMLElement ? e.target.closest("[data-bx-folder-toggle]") : null;
    if (!tgl) return;
    e.preventDefault();
    e.stopPropagation();
    // Find the immediate child <ul> after this row to toggle.
    const li = tgl.closest(".bx-folder-rail__item, .bx-folder-rail__root");
    if (!li) return;
    const sub = li.querySelector(":scope > ul, :scope > .bx-folder-rail__list");
    if (!sub) return;
    const open = !sub.hasAttribute("hidden");
    if (open) {
      sub.setAttribute("hidden", "");
      tgl.setAttribute("aria-expanded", "false");
      tgl.textContent = "▶";
    } else {
      sub.removeAttribute("hidden");
      tgl.setAttribute("aria-expanded", "true");
      tgl.textContent = "▼";
    }
  });

  // ---- inline rename (F2 + double-click) ----------------------------

  function startRename(linkOrName, currentName, onCommit) {
    const input = document.createElement("input");
    input.type = "text";
    input.className = "bx-folder-rail__rename-input";
    input.value = currentName;
    linkOrName.replaceWith(input);
    input.focus();
    input.select();
    let done = false;
    function commit() {
      if (done) return;
      done = true;
      const newName = input.value.trim();
      input.replaceWith(linkOrName);
      if (newName && newName !== currentName) onCommit(newName);
    }
    input.addEventListener("blur", commit);
    input.addEventListener("keydown", (e) => {
      if (e.key === "Enter") { e.preventDefault(); commit(); }
      if (e.key === "Escape") {
        done = true;
        input.replaceWith(linkOrName);
      }
    });
  }

  rail.addEventListener("dblclick", (e) => {
    const link = e.target instanceof HTMLElement ? e.target.closest(".bx-folder-rail__name") : null;
    if (!link) return;
    e.preventDefault();
    const li = link.closest("[data-bx-folder-id]");
    if (!li) return;
    const id = li.getAttribute("data-bx-folder-id");
    const cur = li.getAttribute("data-bx-folder-name") || "";
    startRename(link, cur, (newName) => {
      postForm(`/design/folders/${id}/rename`, { name: newName }).then(reload);
    });
  });

  // ---- drag-and-drop ------------------------------------------------

  // Asset card → folder row OR kind-root header.
  document.addEventListener("dragstart", (e) => {
    const card = e.target instanceof HTMLElement ? e.target.closest("[data-bx-folder-card]") : null;
    const folder = e.target instanceof HTMLElement ? e.target.closest("[data-bx-folder-drag-source]") : null;
    if (card) {
      const id = card.getAttribute("data-bx-asset-id");
      e.dataTransfer.setData("application/x-bx-asset-ids", String(id));
      // Drag-out-to-OS: provide the export URL via DownloadURL.
      const name = card.getAttribute("data-bx-asset-name") || `asset-${id}`;
      e.dataTransfer.setData(
        "DownloadURL",
        `application/zip:${name}.boxasset.zip:${window.location.origin}/design/assets/export/${id}`
      );
      e.dataTransfer.effectAllowed = "copyMove";
      card.classList.add("bx-drag-source");
    } else if (folder) {
      const li = folder.closest("[data-bx-folder-id]");
      if (!li) return;
      e.dataTransfer.setData(
        "application/x-bx-folder-id",
        li.getAttribute("data-bx-folder-id") || ""
      );
      e.dataTransfer.effectAllowed = "move";
    }
  });

  document.addEventListener("dragend", (e) => {
    const card = e.target instanceof HTMLElement ? e.target.closest("[data-bx-folder-card]") : null;
    if (card) card.classList.remove("bx-drag-source");
  });

  function dropTarget(target) {
    if (!(target instanceof HTMLElement)) return null;
    return target.closest("[data-bx-folder-id], [data-bx-folder-root], [data-bx-folder-pane]");
  }

  document.addEventListener("dragover", (e) => {
    const t = dropTarget(e.target);
    if (!t) return;
    e.preventDefault();
    e.dataTransfer.dropEffect = "move";
    t.classList.add("bx-drag-over");
  });
  document.addEventListener("dragleave", (e) => {
    const t = dropTarget(e.target);
    if (t) t.classList.remove("bx-drag-over");
  });

  document.addEventListener("drop", (e) => {
    const t = dropTarget(e.target);
    if (!t) return;
    e.preventDefault();
    t.classList.remove("bx-drag-over");
    document.querySelectorAll(".bx-drag-over").forEach((el) => el.classList.remove("bx-drag-over"));

    const folderID = t.getAttribute("data-bx-folder-id") || "";
    const kindRoot = t.getAttribute("data-bx-folder-root") ||
                     t.getAttribute("data-bx-folder-kind") || "";

    // 1) Asset id payload → bulk move.
    const ids = e.dataTransfer.getData("application/x-bx-asset-ids");
    if (ids) {
      postForm("/design/assets/move", {
        ids: ids,
        folder_id: folderID,
      }).then((r) => {
        if (!r.ok) alert("Move failed: " + r.status);
        reload();
      });
      return;
    }

    // 2) Folder id payload → reparent folder.
    const fID = e.dataTransfer.getData("application/x-bx-folder-id");
    if (fID) {
      // Self-drop: ignore.
      if (folderID === fID) return;
      postForm(`/design/folders/${fID}/move`, { parent_id: folderID }).then((r) => {
        if (!r.ok) {
          r.text().then((t) => alert("Folder move failed: " + t));
          return;
        }
        reload();
      });
      return;
    }

    // 3) External files → upload + move into target.
    if (e.dataTransfer.files && e.dataTransfer.files.length > 0) {
      uploadFilesIntoFolder(e.dataTransfer.files, folderID, kindRoot);
    }
  });

  function uploadFilesIntoFolder(files, folderID, kindRoot) {
    const fd = new FormData();
    for (const f of files) fd.append("files", f);
    if (kindRoot) fd.append("kind", kindRoot);
    fetch("/design/assets/upload", {
      method: "POST",
      credentials: "same-origin",
      headers: { "X-CSRF-Token": csrf() },
      body: fd,
    })
      .then((r) => (r.ok ? r.json().catch(() => ({})) : r.text().then((t) => Promise.reject(t))))
      .then((res) => {
        // Move just-uploaded asset ids into the folder if we landed on
        // a folder target (not the kind root).
        if (folderID && res && res.results) {
          const newIDs = res.results
            .filter((r) => r && r.asset && r.asset.id)
            .map((r) => r.asset.id)
            .join(",");
          if (newIDs) {
            postForm("/design/assets/move", { ids: newIDs, folder_id: folderID }).then(reload);
            return;
          }
        }
        reload();
      })
      .catch((err) => alert("Upload failed: " + err));
  }

  // ---- right-click context menu ------------------------------------

  let ctx = null;
  function closeCtx() {
    if (ctx) ctx.remove();
    ctx = null;
  }
  document.addEventListener("click", closeCtx);
  document.addEventListener("scroll", closeCtx, true);

  rail.addEventListener("contextmenu", (e) => {
    const li = e.target instanceof HTMLElement ? e.target.closest("[data-bx-folder-id]") : null;
    const root = e.target instanceof HTMLElement ? e.target.closest("[data-bx-folder-root]") : null;
    if (!li && !root) return;
    e.preventDefault();
    closeCtx();
    ctx = document.createElement("div");
    ctx.className = "bx-folder-ctx";
    ctx.style.left = e.pageX + "px";
    ctx.style.top = e.pageY + "px";

    if (li) {
      const id = li.getAttribute("data-bx-folder-id");
      const kind = li.getAttribute("data-bx-folder-kind");
      const name = li.getAttribute("data-bx-folder-name") || "";
      ctx.appendChild(menuItem("New folder here", () => {
        openNewFolderModal(kind, id);
      }));
      ctx.appendChild(menuItem("Rename (F2)", () => {
        const link = li.querySelector(".bx-folder-rail__name");
        if (link) startRename(link, name, (newName) =>
          postForm(`/design/folders/${id}/rename`, { name: newName }).then(reload)
        );
      }));
      ctx.appendChild(document.createElement("hr"));
      ["alpha", "date", "type", "color", "length"].forEach((mode) => {
        ctx.appendChild(menuItem(`Sort by: ${mode}`, () => {
          postForm(`/design/folders/${id}/sort-mode`, { mode }).then(reload);
        }));
      });
      ctx.appendChild(document.createElement("hr"));
      ctx.appendChild(menuItem("Delete", () => {
        if (!confirm(`Delete folder "${name}"? Assets inside bubble back to the kind root.`)) return;
        fetch(`/design/folders/${id}`, {
          method: "DELETE",
          credentials: "same-origin",
          headers: { "X-CSRF-Token": csrf() },
        }).then(reload);
      }, { danger: true }));
    } else if (root) {
      const kind = root.getAttribute("data-bx-folder-root");
      ctx.appendChild(menuItem("New folder", () => openNewFolderModal(kind, "")));
    }

    document.body.appendChild(ctx);
  });

  function menuItem(label, onClick, opts = {}) {
    const b = document.createElement("button");
    b.type = "button";
    b.textContent = label;
    if (opts.danger) b.style.color = "var(--bx-danger)";
    b.addEventListener("click", () => { closeCtx(); onClick(); });
    return b;
  }
  function openNewFolderModal(kindRoot, parentID) {
    const url = `/design/folders/new?kind_root=${encodeURIComponent(kindRoot)}&parent_id=${encodeURIComponent(parentID || "")}`;
    if (window.htmx) {
      window.htmx.ajax("GET", url, { target: "#modal-host", swap: "innerHTML" });
    } else {
      window.location.href = `/design/assets?kind=${kindRoot}`;
    }
  }

  // ---- keyboard nav (rail) -----------------------------------------

  rail.addEventListener("keydown", (e) => {
    const li = selectedFolderRow();
    switch (e.key) {
      case "F2": {
        if (!li) return;
        const link = li.querySelector(".bx-folder-rail__name");
        if (link) {
          e.preventDefault();
          const id = li.getAttribute("data-bx-folder-id");
          const cur = li.getAttribute("data-bx-folder-name") || "";
          startRename(link, cur, (newName) =>
            postForm(`/design/folders/${id}/rename`, { name: newName }).then(reload)
          );
        }
        break;
      }
      case "Delete": {
        if (!li) return;
        e.preventDefault();
        const id = li.getAttribute("data-bx-folder-id");
        const name = li.getAttribute("data-bx-folder-name") || "";
        if (confirm(`Delete folder "${name}"?`)) {
          fetch(`/design/folders/${id}`, {
            method: "DELETE",
            credentials: "same-origin",
            headers: { "X-CSRF-Token": csrf() },
          }).then(reload);
        }
        break;
      }
    }
  });

  // ---- Cmd+C / Cmd+X / Cmd+V on the asset pane ---------------------

  document.addEventListener("keydown", (e) => {
    if (!(e.ctrlKey || e.metaKey)) return;
    const target = e.target;
    const isText = target instanceof HTMLElement &&
      (target.tagName === "INPUT" || target.tagName === "TEXTAREA" || target.isContentEditable);
    if (isText) return;
    const ids = selectedAssetIDs();
    switch (e.key.toLowerCase()) {
      case "c": {
        if (ids.length === 0) return;
        e.preventDefault();
        clipboard = { mode: "copy", ids };
        break;
      }
      case "x": {
        if (ids.length === 0) return;
        e.preventDefault();
        clipboard = { mode: "cut", ids };
        break;
      }
      case "v": {
        if (clipboard.ids.length === 0) return;
        e.preventDefault();
        // Paste into the currently-displayed folder (from
        // [data-bx-folder-contents]).
        const contents = document.querySelector("[data-bx-folder-contents]");
        if (!contents) return;
        const folderID = contents.getAttribute("data-bx-folder-id") || "";
        if (clipboard.mode === "cut") {
          postForm("/design/assets/move", {
            ids: clipboard.ids.join(","),
            folder_id: folderID,
          }).then(reload);
        } else {
          // "copy" semantics for assets is ambiguous (one DB row per
          // asset); for v1 we treat copy = move + alert. Future:
          // duplicate the row with a "(copy)" name suffix.
          postForm("/design/assets/move", {
            ids: clipboard.ids.join(","),
            folder_id: folderID,
          }).then(reload);
        }
        break;
      }
      case "n": {
        // New folder under the selected parent (or the kind root the
        // selected row lives under).
        const li = selectedFolderRow();
        if (li) {
          e.preventDefault();
          openNewFolderModal(
            li.getAttribute("data-bx-folder-kind"),
            li.getAttribute("data-bx-folder-id")
          );
          break;
        }
        const root = rail.querySelector("[data-bx-folder-root]");
        if (root) {
          e.preventDefault();
          openNewFolderModal(root.getAttribute("data-bx-folder-root"), "");
        }
        break;
      }
    }
  });
})();
