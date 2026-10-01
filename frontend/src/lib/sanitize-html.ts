import DOMPurify from "dompurify";

// Product descriptions are vendor- or scraper-supplied HTML. sanitizeDescription
// keeps formatting (text, lists, tables, images) and removes everything that
// can act: scripts and event handlers (DOMPurify defaults), forms and inputs
// (a fake login box inside a description), embedded frames and styles. Links
// open in a new tab without access to this page; images and links must use
// http(s). Needs a DOM: call it in the browser only.
const FORBID_TAGS = [
  "form",
  "input",
  "button",
  "textarea",
  "select",
  "option",
  "style",
  "iframe",
  "frame",
  "object",
  "embed",
  "svg",
  "math",
];

let hooked = false;

function hook() {
  if (hooked) return;
  hooked = true;
  DOMPurify.addHook("afterSanitizeAttributes", (node) => {
    if (node.tagName === "A") {
      const href = node.getAttribute("href") ?? "";
      if (!/^https?:\/\//i.test(href)) node.removeAttribute("href");
      node.setAttribute("target", "_blank");
      node.setAttribute("rel", "noopener noreferrer nofollow ugc");
    }
    if (node.tagName === "IMG") {
      const src = node.getAttribute("src") ?? "";
      if (!/^https?:\/\//i.test(src)) node.remove();
      else {
        node.setAttribute("loading", "lazy");
        node.setAttribute("referrerpolicy", "no-referrer");
        node.removeAttribute("srcset");
      }
    }
  });
}

export function sanitizeDescription(html: string): string {
  if (typeof window === "undefined") return "";
  hook();
  return DOMPurify.sanitize(html, {
    FORBID_TAGS,
    FORBID_ATTR: ["style", "class", "id", "formaction"],
    ALLOW_DATA_ATTR: false,
  });
}
