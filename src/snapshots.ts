import type { Config } from "./config";
import type {
  Dictionary,
  ItemSnapshot,
  JsonRecord,
  SnapshotRun,
} from "./models";
import { isRecord } from "./models";
import { readTextIfExists, writeMonthlyArchive } from "./storage";
import { fetchStatisticsText, parseStatistics } from "./warframe-market";

const HOUR_MS = 60 * 60 * 1_000;
const DAY_MS = 24 * HOUR_MS;

export async function buildSnapshotRun(
  dictionary: Dictionary,
  config: Config,
): Promise<SnapshotRun> {
  const cachedSnapshots = await loadCachedSnapshots(config.snapshotPath);
  const runStartedAt = new Date();
  const selectedItems = dictionary.tradeable_items.slice(
    config.fetchOffset,
    config.fetchLimit === undefined
      ? undefined
      : config.fetchOffset + config.fetchLimit,
  );
  const snapshots: ItemSnapshot[] = [];
  const minimumRequestInterval = 1_000 / config.requestsPerSecond;
  let previousRequestStartedAt: number | undefined;

  for (const { slug } of selectedItems) {
    const cached = cachedSnapshots.get(slug);
    if (cached && isSnapshotFresh(cached, new Date())) {
      console.log(`(skip) slug=${slug} liquidity=${cached.liquidity}`);
      snapshots.push(cached);
      continue;
    }

    if (previousRequestStartedAt !== undefined) {
      const elapsed = performance.now() - previousRequestStartedAt;
      const delay = minimumRequestInterval - elapsed;
      if (delay > 0) await Bun.sleep(delay);
    }

    const fetchedAt = new Date();
    previousRequestStartedAt = performance.now();
    const body = await fetchStatisticsText(slug, config.requestTimeoutMs);

    try {
      await writeMonthlyArchive(config.archiveDirectory, slug, fetchedAt, body);
    } catch (error) {
      console.error(`failed to archive statistics for slug ${slug}:`, error);
    }

    const statistics = parseStatistics(body, slug);
    const today = utcDate(fetchedAt);
    const yesterday = utcDate(new Date(fetchedAt.getTime() - DAY_MS));
    const statisticsToday = closedRowsForDay(statistics.closed, today);
    const statisticsYesterday = closedRowsForDay(statistics.closed, yesterday);
    const currentOffers = currentHourSellRows(statistics.live, fetchedAt);
    const liquidity =
      sumVolumes(statisticsYesterday) + sumVolumes(statisticsToday);

    console.log(`(fetch) slug=${slug} liquidity=${liquidity}`);
    snapshots.push({
      slug,
      last_fetched_at: fetchedAt.toISOString(),
      liquidity,
      statistics_yesterday: statisticsYesterday,
      statistics_today: statisticsToday,
      current_offers: currentOffers,
    });
  }

  return {
    run_start: runStartedAt.toISOString(),
    run_end: new Date().toISOString(),
    tradeable_items: snapshots,
  };
}

async function loadCachedSnapshots(
  path: string,
): Promise<Map<string, ItemSnapshot>> {
  const body = await readTextIfExists(path);
  if (body === undefined) return new Map();

  try {
    const run = parseSnapshotRun(JSON.parse(body));
    return new Map(run.tradeable_items.map((item) => [item.slug, item]));
  } catch {
    // A malformed previous run is equivalent to an empty cache.
    return new Map();
  }
}

function parseSnapshotRun(value: unknown): SnapshotRun {
  if (
    !isRecord(value) ||
    typeof value.run_start !== "string" ||
    (value.run_end !== undefined && typeof value.run_end !== "string") ||
    !Array.isArray(value.tradeable_items)
  ) {
    throw new Error("snapshot run has an invalid shape");
  }

  for (const item of value.tradeable_items) {
    if (
      !isRecord(item) ||
      typeof item.slug !== "string" ||
      typeof item.last_fetched_at !== "string" ||
      typeof item.liquidity !== "number" ||
      !Number.isSafeInteger(item.liquidity) ||
      item.liquidity < 0 ||
      !Array.isArray(item.statistics_yesterday) ||
      !Array.isArray(item.statistics_today) ||
      !Array.isArray(item.current_offers)
    ) {
      throw new Error("snapshot item has an invalid shape");
    }
  }
  return value as unknown as SnapshotRun;
}

function isSnapshotFresh(item: ItemSnapshot, now: Date): boolean {
  const fetchedAt = Date.parse(item.last_fetched_at);
  if (Number.isNaN(fetchedAt)) return false;
  const elapsedHours = Math.round(
    Math.max(0, now.getTime() - fetchedAt) / HOUR_MS,
  );
  return elapsedHours < freshnessHours(item.liquidity);
}

function freshnessHours(liquidity: number): number {
  if (liquidity <= 20) return 24;
  if (liquidity <= 100) return 6;
  return 1;
}

function closedRowsForDay(entries: unknown[], day: string): JsonRecord[] {
  return entries
    .filter(isRecord)
    .filter((entry) => {
      const timestamp = parseTimestamp(entry.datetime);
      // Closed intervals are labeled by their start, one day before their effective date.
      return (
        timestamp !== undefined && utcDate(new Date(timestamp + DAY_MS)) === day
      );
    })
    .map(stripInternalFields);
}

function currentHourSellRows(entries: unknown[], now: Date): JsonRecord[] {
  const currentHour = Math.floor(now.getTime() / HOUR_MS);
  return entries
    .filter(isRecord)
    .filter((entry) => {
      const timestamp = parseTimestamp(entry.datetime);
      // Live intervals are labeled by their start, one hour before their effective hour.
      return (
        entry.order_type === "sell" &&
        timestamp !== undefined &&
        Math.floor((timestamp + HOUR_MS) / HOUR_MS) === currentHour
      );
    })
    .map(stripInternalFields);
}

function stripInternalFields(entry: JsonRecord): JsonRecord {
  const {
    id: _id,
    datetime: _datetime,
    order_type: _orderType,
    ...publicFields
  } = entry;
  return publicFields;
}

function sumVolumes(entries: JsonRecord[]): number {
  return entries.reduce((total, entry) => {
    const volume = entry.volume;
    const valid =
      typeof volume === "number" && Number.isInteger(volume) && volume >= 0;
    return total + (valid ? volume : 0);
  }, 0);
}

function parseTimestamp(value: unknown): number | undefined {
  if (typeof value !== "string") return undefined;
  const timestamp = Date.parse(value);
  return Number.isNaN(timestamp) ? undefined : timestamp;
}

function utcDate(date: Date): string {
  return date.toISOString().slice(0, 10);
}
