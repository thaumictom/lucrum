import { join } from "node:path";

const DATA_DIRECTORY = "data";

export interface Config {
  dictionaryPath: string;
  snapshotPath: string;
  archiveDirectory: string;
  dictionaryMaxAgeMs: number;
  requestTimeoutMs: number;
  requestsPerSecond: number;
  fetchOffset: number;
  fetchLimit?: number;
}

export function loadConfig(env: NodeJS.ProcessEnv = process.env): Config {
  return {
    dictionaryPath: join(DATA_DIRECTORY, "dictionary.json"),
    snapshotPath: join(DATA_DIRECTORY, "tradeable_items.json"),
    archiveDirectory: join(DATA_DIRECTORY, "archive"),
    dictionaryMaxAgeMs: 24 * 60 * 60 * 1_000,
    requestTimeoutMs: 60_000,
    requestsPerSecond: positiveNumber(
      env.LUCRUM_REQUESTS_PER_SECOND,
      "LUCRUM_REQUESTS_PER_SECOND",
      2.5,
    ),
    fetchOffset: nonnegativeInteger(
      env.LUCRUM_FETCH_OFFSET,
      "LUCRUM_FETCH_OFFSET",
      0,
    ),
    fetchLimit: optionalLimit(env.LUCRUM_FETCH_LIMIT),
  };
}

function positiveNumber(
  raw: string | undefined,
  name: string,
  fallback: number,
): number {
  if (raw === undefined) return fallback;
  const value = Number(raw);
  if (!Number.isFinite(value) || value <= 0) {
    throw new Error(`${name} must be a finite number greater than zero`);
  }
  return value;
}

function nonnegativeInteger(
  raw: string | undefined,
  name: string,
  fallback: number,
): number {
  if (raw === undefined) return fallback;
  if (!/^\d+$/.test(raw))
    throw new Error(`${name} must be a nonnegative integer`);
  const value = Number(raw);
  if (!Number.isSafeInteger(value))
    throw new Error(`${name} must be a safe integer`);
  return value;
}

function optionalLimit(raw: string | undefined): number | undefined {
  const value = nonnegativeInteger(raw, "LUCRUM_FETCH_LIMIT", 20);
  // Zero intentionally disables the default limit.
  return value === 0 ? undefined : value;
}
