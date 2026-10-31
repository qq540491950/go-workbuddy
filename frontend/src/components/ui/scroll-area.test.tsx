import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { ScrollArea } from "./scroll-area";

// The root must keep overflow-hidden: the chat column places this component
// as a flex-1 child of a fixed-height layout, and only a non-visible overflow
// lets flexbox shrink it below the content height. Without it, long chats
// push the composer out of the window and the area cannot scroll.
describe("ScrollArea", () => {
  it("renders the root with overflow-hidden so flex parents can shrink it", () => {
    const html = renderToStaticMarkup(
      <ScrollArea className="flex-1">
        <div>content</div>
      </ScrollArea>,
    );
    const rootTag = html.slice(0, html.indexOf(">") + 1);
    expect(rootTag).toContain('data-slot="scroll-area"');
    expect(rootTag).toContain("overflow-hidden");
  });
});
