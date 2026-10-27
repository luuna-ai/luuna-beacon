import { createHash } from "node:crypto";
import { spawnSync } from "node:child_process";
import { existsSync, mkdirSync, readFileSync, readdirSync, renameSync, rmSync, writeFileSync } from "node:fs";
import { join, resolve } from "node:path";

const root = resolve(import.meta.dir, "..");
const cacheDir = join(root, ".beacon-bin");
const binary = join(cacheDir, process.platform === "win32" ? "luuna-beacon.exe" : "luuna-beacon");
const stamp = `${binary}.source`;

function sourceDigest(): string {
	const hash = createHash("sha256");
	for (const name of ["go.mod", "go.sum"]) hash.update(readFileSync(join(root, name)));
	for (const directory of ["cmd", "internal"]) {
		const walk = (path: string): void => {
			for (const entry of readdirSync(path, { withFileTypes: true }).sort((a, b) => a.name.localeCompare(b.name))) {
				const file = join(path, entry.name);
				if (entry.isDirectory()) walk(file);
				else if (entry.name.endsWith(".go")) hash.update(file.slice(root.length)).update(readFileSync(file));
			}
		};
		walk(join(root, directory));
	}
	return hash.digest("hex");
}

function goExecutable(): string {
	const go = process.env.LUUNA_BEACON_GO || Bun.which("go");
	if (!go) throw new Error("Building Luuna Beacon requires Go 1.27.0 or newer; install Go or set LUUNA_BEACON_GO.");
	return go;
}

function ensureNativeBinary(): string {
	const expected = sourceDigest();
	if (existsSync(binary) && existsSync(stamp) && readFileSync(stamp, "utf8").trim() === expected) return binary;
	mkdirSync(cacheDir, { recursive: true });
	const temporary = `${binary}.tmp-${process.pid}`;
	try {
		const result = spawnSync(
			goExecutable(),
			["build", "-trimpath", "-buildvcs=false", "-o", temporary, "./cmd/luuna-beacon"],
			{ cwd: root, encoding: "utf8", env: { ...process.env, CGO_ENABLED: "0", GOTOOLCHAIN: "local" } },
		);
		if (result.status !== 0) throw new Error(result.stderr.trim() || "Native Luuna Beacon build failed.");
		renameSync(temporary, binary);
		writeFileSync(stamp, `${expected}\n`);
		return binary;
	} finally {
		rmSync(temporary, { force: true });
	}
}

const child = spawnSync(ensureNativeBinary(), process.argv.slice(2), {
	cwd: process.cwd(),
	stdio: "inherit",
	env: process.env,
});
if (child.error) throw child.error;
process.exitCode = child.status ?? 1;
