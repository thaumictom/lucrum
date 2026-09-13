export type JsonRecord = Record<string, unknown>;

export interface DictionaryItem {
  slug: string;
  name: string;
  tags: string[];
  gameRef?: string;
  set_slug?: string;
  maxRank?: number;
  vaulted?: boolean;
  ducats?: number;
}

export interface Dictionary {
  last_fetched_at: string;
  tradeable_items: DictionaryItem[];
}

export interface ItemSnapshot {
  slug: string;
  last_fetched_at: string;
  liquidity: number;
  statistics_yesterday: JsonRecord[];
  statistics_today: JsonRecord[];
  current_offers: JsonRecord[];
}

export interface SnapshotRun {
  run_start: string;
  run_end: string;
  tradeable_items: ItemSnapshot[];
}

export function isRecord(value: unknown): value is JsonRecord {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

export function isOptionalU32(value: unknown): boolean {
  return (
    value === undefined ||
    value === null ||
    (typeof value === "number" &&
      Number.isInteger(value) &&
      value >= 0 &&
      value <= 0xffff_ffff)
  );
}
