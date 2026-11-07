// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Source an instrumented document may include itself. It does nothing unless
// the document sets data-dropway-review="1". It never reads storage. The
// parent replaces the placeholder target with the preview it actually opened.
// A quote that is missing or duplicated becomes a whole-page selection.
// The serving worker does not inject this script.

const GUEST_BRIDGE_SOURCE = [
  "(function () {",
  '  if (!document.documentElement || document.documentElement.getAttribute("data-dropway-review") !== "1") return;',
  '  var CHANNEL = "dropway.collab.v1";',
  '  var nonce = "";',
  "  function pagePath() {",
  '    var path = "/";',
  "    try {",
  '      var raw = String(location.pathname || "/");',
  '      if (raw.charAt(0) === "/" && raw.indexOf("//") !== 0 && raw.indexOf("\\\\") < 0 && raw.indexOf("?") < 0 && raw.indexOf("#") < 0 && raw.length <= 512) path = raw;',
  "    } catch (err) {}",
  "    return path;",
  "  }",
  "  function target() {",
  '    return { siteId: "preview", versionId: "preview", contentHash: "preview", page: pagePath() };',
  "  }",
  '  window.addEventListener("message", function (event) {',
  "    if (event.source !== window.parent || !event.data) return;",
  "    var data = event.data;",
  '    if (data.channel !== CHANNEL || data.type !== "ready" || typeof data.nonce !== "string") return;',
  "    nonce = data.nonce;",
  "  });",
  "  function post(type, body) {",
  "    if (!nonce || !window.parent || window.parent === window) return;",
  "    var message = { channel: CHANNEL, nonce: nonce, type: type };",
  "    if (body) {",
  "      for (var key in body) {",
  "        if (Object.prototype.hasOwnProperty.call(body, key)) message[key] = body[key];",
  "      }",
  "    }",
  '    window.parent.postMessage(message, "*");',
  "  }",
  "  function postPage() {",
  '    var anchor = target();',
  '    anchor.kind = "page";',
  '    post("selection", { anchor: anchor });',
  "  }",
  '  window.addEventListener("mouseup", function () {',
  "    var selection = window.getSelection && window.getSelection();",
  "    if (!selection || selection.isCollapsed) return;",
  "    var exact = String(selection);",
  "    if (!exact || exact.length > 2000) { postPage(); return; }",
  '    var text = document.body ? String(document.body.innerText || "") : "";',
  "    var start = text.indexOf(exact);",
  "    if (start < 0 || text.indexOf(exact, start + 1) !== -1) { postPage(); return; }",
  "    var prefix = text.slice(Math.max(0, start - 32), start);",
  "    var suffix = text.slice(start + exact.length, start + exact.length + 32);",
  "    var anchor = target();",
  '    anchor.kind = "text";',
  "    anchor.exact = exact;",
  "    anchor.prefix = prefix;",
  "    anchor.suffix = suffix;",
  "    anchor.start = start;",
  "    anchor.end = start + exact.length;",
  '    post("selection", { anchor: anchor });',
  "  });",
  "})();",
].join("\n");

export function guestBridgeSource(): string {
  return GUEST_BRIDGE_SOURCE;
}
