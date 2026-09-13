import { isOptionalU32, isRecord } from "./models";

const API_ORIGIN = "https://api.warframe.market";

interface ApiItem {
  slug: string;
  maxRank?: number | null;
  vaulted?: boolean | null;
  ducats?: number | null;
  tags?: string[];
  i18n?: { en?: { name?: string | null } };
}

export interface StatisticsPayload {
  closed: unknown[];
  live: unknown[];
}

export async function fetchItems(timeoutMs: number): Promise<ApiItem[]> {
  const body = await fetchText(`${API_ORIGIN}/v2/items`, timeoutMs, "items");
  let value: unknown;
  try {
    value = JSON.parse(body);
  } catch (error) {
    throw new Error("failed to parse items response JSON", { cause: error });
  }

  if (!isRecord(value) || !Array.isArray(value.data)) {
    throw new Error("items response must contain a data array");
  }
  return value.data.map(parseApiItem);
}

export async function fetchStatisticsText(
  slug: string,
  timeoutMs: number,
): Promise<string> {
  return fetchText(
    `${API_ORIGIN}/v1/items/${slug}/statistics`,
    timeoutMs,
    `statistics for slug ${slug}`,
  );
}

export function parseStatistics(body: string, slug: string): StatisticsPayload {
  let value: unknown;
  try {
    value = JSON.parse(body);
  } catch (error) {
    throw new Error(`failed to parse statistics payload for slug ${slug}`, {
      cause: error,
    });
  }

  if (!isRecord(value) || !isRecord(value.payload)) {
    throw new Error(`statistics payload has an invalid shape for slug ${slug}`);
  }

  const closedContainer = value.payload.statistics_closed;
  const liveContainer = value.payload.statistics_live;
  if (
    (closedContainer !== undefined && !isRecord(closedContainer)) ||
    (liveContainer !== undefined && !isRecord(liveContainer))
  ) {
    throw new Error(`statistics payload has an invalid shape for slug ${slug}`);
  }

  const closed = closedContainer?.["90days"];
  const live = liveContainer?.["48hours"];
  if (
    (closed !== undefined && !Array.isArray(closed)) ||
    (live !== undefined && !Array.isArray(live))
  ) {
    throw new Error(`statistics payload has an invalid shape for slug ${slug}`);
  }

  return { closed: closed ?? [], live: live ?? [] };
}

function parseApiItem(value: unknown): ApiItem {
  if (!isRecord(value) || typeof value.slug !== "string") {
    throw new Error("every item must contain a string slug");
  }
  if (
    value.tags !== undefined &&
    (!Array.isArray(value.tags) ||
      !value.tags.every((tag) => typeof tag === "string"))
  ) {
    throw new Error(`item ${value.slug} has invalid tags`);
  }
  if (!isOptionalU32(value.maxRank) || !isOptionalU32(value.ducats)) {
    throw new Error(`item ${value.slug} has invalid numeric metadata`);
  }
  if (
    value.vaulted !== undefined &&
    value.vaulted !== null &&
    typeof value.vaulted !== "boolean"
  ) {
    throw new Error(`item ${value.slug} has invalid vaulted metadata`);
  }
  if (value.i18n !== undefined) {
    if (!isRecord(value.i18n)) {
      throw new Error(`item ${value.slug} has invalid i18n metadata`);
    }
    if (value.i18n.en !== undefined) {
      if (!isRecord(value.i18n.en)) {
        throw new Error(`item ${value.slug} has invalid English metadata`);
      }
      const name = value.i18n.en.name;
      if (name !== undefined && name !== null && typeof name !== "string") {
        throw new Error(`item ${value.slug} has an invalid English name`);
      }
    }
  }
  return value as unknown as ApiItem;
}

async function fetchText(
  url: string,
  timeoutMs: number,
  label: string,
): Promise<string> {
  const response = await fetch(url, { signal: AbortSignal.timeout(timeoutMs) });
  if (!response.ok) {
    throw new Error(
      `${label} request returned ${response.status} ${response.statusText}`,
    );
  }
  return response.text();
}
