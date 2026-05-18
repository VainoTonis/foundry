import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { Type } from "typebox";
import { writeFileSync } from "node:fs";
import { join } from "node:path";

export default function (pi: ExtensionAPI) {
  pi.registerTool({
    name: "update_spec",
    label: "Update Spec",
    description: "Emit the current spec markdown. Call this whenever the spec changes.",
    parameters: Type.Object({
      content: Type.String({ description: "Full spec markdown content" }),
    }),
    async execute(toolCallId, params, signal, onUpdate, ctx) {
      const outPath = join(ctx.cwd, ".foundry-spec.md");
      writeFileSync(outPath, params.content, "utf8");
      return {
        content: [{ type: "text", text: "spec updated" }],
        details: { path: outPath },
      };
    },
  });
}
