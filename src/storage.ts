import { mkdir, readFile, writeFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { gzipSync } from "node:zlib";
import { isRecord } from "./models";

export async function readJson(path: string): Promise<unknown> {
  return JSON.parse(await readFile(path, "utf8"));
}

export async function readTextIfExists(
  path: string,
): Promise<string | undefined> {
  try {
    return await readFile(path, "utf8");
  } catch (error) {
    if (isRecord(error) && error.code === "ENOENT") return undefined;
    throw error;
  }
}

export async function writeJson(path: string, value: unknown): Promise<void> {
  await mkdir(dirname(path), { recursive: true });
  await writeFile(path, JSON.stringify(value, null, 2), "utf8");
}

export async function writeMonthlyArchive(
  archiveDirectory: string,
  slug: string,
  fetchedAt: Date,
  body: string,
): Promise<void> {
  const monthDirectory = join(
    archiveDirectory,
    fetchedAt.toISOString().slice(0, 7),
  );
  const path = join(monthDirectory, `${slug}.json.gz`);
  await mkdir(monthDirectory, { recursive: true });

  try {
    // Exclusive creation keeps the first response for a slug in each UTC month.
    await writeFile(path, gzipSync(body), { flag: "wx" });
  } catch (error) {
    if (isRecord(error) && error.code === "EEXIST") return;
    throw error;
  }
}
