// Tiki-scraped descriptions interleave genuinely useful images (size charts)
// with marketing/lifestyle photos, inside a flat sequence of <p> tags with no
// structural markup distinguishing the two -- the only signal available is
// nearby text. Bias toward keeping an image on doubt: a missed marketing
// photo is a minor annoyance, a removed size chart is lost information.
const SIZE_KEYWORDS = [
  "size",
  "kích thước",
  "kích cỡ",
  "bảng size",
  "size chart",
  "bảng đo",
  "sơ đồ đo",
  "quy đổi size",
  "chọn size",
];

// Exported separately so the keyword heuristic itself is unit-testable
// without a DOM (removeNonSizeImages below needs DOMParser, same client-only
// constraint DOMPurify already has in product/[slug]/page.tsx).
export function isSizeRelatedContext(text: string): boolean {
  const normalized = text.normalize("NFC").toLowerCase();
  return SIZE_KEYWORDS.some((keyword) => normalized.includes(keyword));
}

const CONTEXT_HOPS = 2;

// Tiki descriptions are riddled with blank spacer paragraphs ("<p> </p>")
// between real content blocks, so the literal previous/next sibling is often
// empty -- walk past those (without walking arbitrarily far) to find the
// nearest block that actually has text.
function nearbyText(start: Element, direction: "previousElementSibling" | "nextElementSibling"): string {
  const parts: string[] = [];
  let el = start[direction];
  let hops = 0;
  while (el && hops < CONTEXT_HOPS) {
    const text = el.textContent?.trim();
    if (text) {
      parts.push(text);
      hops += 1;
    }
    el = el[direction];
  }
  return parts.join(" ");
}

export function removeNonSizeImages(html: string): string {
  if (typeof window === "undefined") return html;
  const container = new DOMParser().parseFromString(html, "text/html").body;

  for (const img of Array.from(container.querySelectorAll("img"))) {
    const block = img.closest("p, li, div") ?? img.parentElement;
    const context = [
      block?.textContent,
      block && nearbyText(block, "previousElementSibling"),
      block && nearbyText(block, "nextElementSibling"),
      img.getAttribute("alt"),
    ]
      .filter(Boolean)
      .join(" ");
    if (!isSizeRelatedContext(context)) img.remove();
  }

  // Drop paragraphs that only ever wrapped the image just removed, so no
  // blank gaps are left in its place.
  for (const p of Array.from(container.querySelectorAll("p"))) {
    if (!p.textContent?.trim() && !p.querySelector("img, video, table")) p.remove();
  }

  return container.innerHTML;
}
