import { spawn } from "node:child_process";
import type { ExtensionAPI } from "@mariozechner/pi-coding-agent";
import { Type } from "@sinclair/typebox";

function runAgent(args: string[], cwd: string, signal?: AbortSignal): Promise<string> {
  return new Promise((resolve, reject) => {
    const child = spawn("zcode-agent", args, {
      cwd,
      env: process.env,
      stdio: ["ignore", "pipe", "pipe"],
    });
    let stdout = "";
    let stderr = "";
    child.stdout.on("data", (chunk) => (stdout += chunk.toString()));
    child.stderr.on("data", (chunk) => (stderr += chunk.toString()));
    const abort = () => child.kill("SIGTERM");
    signal?.addEventListener("abort", abort, { once: true });
    child.on("error", reject);
    child.on("close", (code) => {
      signal?.removeEventListener("abort", abort);
      if (code === 0) resolve(stdout.trim());
      else reject(new Error(stderr.trim() || `zcode-agent exited with ${code}`));
    });
  });
}
export default function zcodeProtect(pi: ExtensionAPI) {
  pi.registerTool({
    name: "inspect_repository_snapshot",
    label: "Inspect Git snapshot",
    description: "Read-only inspection of everything that a Git snapshot would include.",
    parameters: Type.Object({
      repository_root: Type.Optional(Type.String({ description: "Defaults to the current workspace." })),
    }),
    async execute(_toolCallId, params, signal, _onUpdate, ctx) {
      const repo = params.repository_root || ctx.cwd;
      const output = await runAgent(["inspect", "--repo", repo], ctx.cwd, signal);
      return { content: [{ type: "text", text: output }], details: {} };
    },
  });

  pi.registerTool({
    name: "upload_repository_snapshot",
    label: "Upload encrypted Git snapshot",
    description: "Packages and uploads the workspace with its complete .git directory using the server confirmed during plugin setup. No per-repository confirmation is required.",
    parameters: Type.Object({
      repository_root: Type.Optional(Type.String()),
      extra_manifest_paths: Type.Optional(Type.Array(Type.String())),
    }),
    async execute(_toolCallId, params, signal, _onUpdate, ctx) {
      const repo = params.repository_root || ctx.cwd;
      const args = ["upload", "--repo", repo];
      for (const path of params.extra_manifest_paths || []) args.push("--extra-manifest", path);
      const output = await runAgent(args, ctx.cwd, signal);
      return { content: [{ type: "text", text: output }], details: {} };
    },
  });
}
