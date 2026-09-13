import { z } from "zod";

const API_ORIGIN = "https://api.warframe.market";

const u32Schema = z.number().int().min(0).max(0xffff_ffff);
const apiItemSchema = z.object({
  slug: z.string().min(1),
  gameRef: z.string().optional(),
  maxRank: u32Schema.nullish(),
  vaulted: z.boolean().nullish(),
  ducats: u32Schema.nullish(),
  tags: z.array(z.string()).optional(),
  i18n: z
    .object({
      en: z
        .object({
          name: z.string().nullish(),
        })
        .optional(),
    })
    .optional(),
});
const itemsResponseSchema = z.object({
  data: z.array(apiItemSchema),
});
const statisticsResponseSchema = z.object({
  payload: z.object({
    statistics_closed: z
      .object({
        "90days": z.array(z.unknown()).optional(),
      })
      .optional(),
    statistics_live: z
      .object({
        "48hours": z.array(z.unknown()).optional(),
      })
      .optional(),
  }),
});

type ApiItem = z.infer<typeof apiItemSchema>;

export interface StatisticsPayload {
  closed: unknown[];
  live: unknown[];
}

export async function fetchItems(timeoutMs: number): Promise<ApiItem[]> {
  const body = await fetchText(`${API_ORIGIN}/v2/items`, timeoutMs, "items");
  const value = parseJson(body, "items response");
  const result = itemsResponseSchema.safeParse(value);
  if (!result.success) {
    throw new Error("items response has an invalid shape", {
      cause: result.error,
    });
  }
  return result.data.data;
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
  const value = parseJson(body, `statistics payload for slug ${slug}`);
  const result = statisticsResponseSchema.safeParse(value);
  if (!result.success) {
    throw new Error(`statistics payload has an invalid shape for slug ${slug}`, {
      cause: result.error,
    });
  }

  return {
    closed: result.data.payload.statistics_closed?.["90days"] ?? [],
    live: result.data.payload.statistics_live?.["48hours"] ?? [],
  };
}

function parseJson(body: string, label: string): unknown {
  try {
    return JSON.parse(body);
  } catch (error) {
    throw new Error(`failed to parse ${label} JSON`, { cause: error });
  }
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
