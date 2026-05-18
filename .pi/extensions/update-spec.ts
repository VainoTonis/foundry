import { ExtensionAPI } from '@earendil-works/pi-coding-agent';
import { Type } from 'typebox';
import { writeFileSync } from 'node:fs';
import { join } from 'node:path';

export default function (api: ExtensionAPI) {
  api.registerTool('update_spec', {
    description: 'Update the spec file with new content',
    parameters: Type.Object({
      content: Type.String({
        description: 'Full spec markdown content'
      })
    }),
    execute: async (params, ctx) => {
      const path = join(ctx.cwd, '.foundry-spec.md');
      writeFileSync(path, params.content, 'utf-8');

      return {
        content: [{ type: 'text', text: 'spec updated' }],
        details: { path }
      };
    }
  });
}
