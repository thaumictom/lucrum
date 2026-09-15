import {
  dictionarySchema,
  environmentSchema,
  itemsSchema,
  statisticsSchema,
  tradeableItemsSchema,
  type Dictionary,
  type Statistic,
  type TradeableItems,
} from './schemas';

const API_URL = 'https://api.warframe.market';
const DICTIONARY_PATH = 'data/dictionary.json';
const TRADEABLE_ITEMS_PATH = 'data/tradeable_items.json';
const HOUR = 60 * 60 * 1000;
const DICTIONARY_CACHE_LIFETIME = 4 * HOUR;

const {
  LUCRUM_REQUESTS_PER_SECOND: requestsPerSecond,
  LUCRUM_ITEM_OFFSET: itemOffset,
  LUCRUM_ITEM_LIMIT: itemLimit,
} = environmentSchema.parse(Bun.env);

let lastRequestAt = 0;

async function request(path: string) {
  const interval = 1000 / requestsPerSecond;
  const wait = interval - (Date.now() - lastRequestAt);
  if (wait > 0) await Bun.sleep(wait);

  lastRequestAt = Date.now();
  const fetchedAt = new Date(lastRequestAt).toISOString();
  const response = await fetch(`${API_URL}${path}`, {
    signal: AbortSignal.timeout(60_000),
  });

  if (!response.ok) {
    throw new Error(`${path}: ${response.status} ${response.statusText}`);
  }

  return { response, fetchedAt };
}

function writeJson(path: string, value: unknown) {
  return Bun.write(path, JSON.stringify(value, null, 2));
}

function isFresh(fetchedAt: string, lifetime: number) {
  return Date.now() - Date.parse(fetchedAt) <= lifetime;
}

async function readCachedDictionary() {
  const file = Bun.file(DICTIONARY_PATH);
  if (!(await file.exists())) return;

  const cached = dictionarySchema.safeParse(await file.json());
  if (cached.success && isFresh(cached.data.fetched_at, DICTIONARY_CACHE_LIFETIME)) {
    return cached.data;
  }
}

async function getDictionary(): Promise<Dictionary> {
  const cached = await readCachedDictionary();
  if (cached) {
    console.log('(skip) dictionary');
    return cached;
  }

  console.log('(fetch) dictionary');
  const { response, fetchedAt } = await request('/v2/items');
  const responseBody = itemsSchema.parse(await response.json());
  const dictionary = {
    ...responseBody,
    fetched_at: fetchedAt,
    data: responseBody.data.map(({ id, i18n, ...item }) => ({
      ...item,
      name: i18n.en.name,
    })),
  };

  await writeJson(DICTIONARY_PATH, dictionary);
  return dictionary;
}

async function readTradeableItems(): Promise<TradeableItems> {
  const file = Bun.file(TRADEABLE_ITEMS_PATH);
  return (await file.exists())
    ? tradeableItemsSchema.parse(await file.json())
    : { data: [] };
}

function patchTradeableItems(
  dictionary: Dictionary,
  current: TradeableItems,
): TradeableItems {
  const currentBySlug = new Map(current.data.map((item) => [item.slug, item]));

  return {
    ...current,
    ...dictionary,
    error: undefined,
    apiVersion: undefined,
    data: dictionary.data.map((item) => ({
      ...currentBySlug.get(item.slug),
      ...item,
      gameRef: undefined,
      tags: undefined,
      subtypes: undefined,
    })),
  };
}

function utcDate(daysAgo: number, now: Date) {
  const date = new Date(now);
  date.setUTCDate(date.getUTCDate() - daysAgo);
  return date.toISOString().slice(0, 10);
}

function selectStatistics(rows: Statistic[], date: string) {
  return rows
    .filter(({ datetime }) => new Date(datetime).toISOString().slice(0, 10) === date)
    .map(({ datetime, id, order_type, ...row }) => row);
}

async function getStatistics(slug: string) {
  const { response, fetchedAt } = await request(`/v1/items/${slug}/statistics`);
  const rows = statisticsSchema.parse(await response.json()).payload.statistics_closed['90days'];
  const now = new Date();
  const statisticsYesterday = selectStatistics(rows, utcDate(2, now));
  const statisticsToday = selectStatistics(rows, utcDate(1, now));
  const liquidity = [...statisticsYesterday, ...statisticsToday].reduce(
    (total, row) => total + row.volume,
    0,
  );

  return {
    fetched_at: fetchedAt,
    statistics_yesterday: statisticsYesterday,
    statistics_today: statisticsToday,
    liquidity,
  };
}

function statisticsCacheLifetime(liquidity: number) {
  if (liquidity <= 20) return 24 * HOUR;
  if (liquidity >= 21 && liquidity <= 150) return 6 * HOUR;
  return 0;
}

function hasFreshStatistics(item: TradeableItems['data'][number]) {
  if (item.fetched_at === undefined || item.liquidity === undefined) return false;

  const lifetime = statisticsCacheLifetime(item.liquidity);
  return lifetime > 0 && isFresh(item.fetched_at, lifetime);
}

function selectItems(items: TradeableItems['data']) {
  const end = itemLimit === 0 ? undefined : itemOffset + itemLimit;
  return items.slice(itemOffset, end);
}

async function updateStatistics(items: TradeableItems['data']) {
  for (const item of selectItems(items)) {
    if (hasFreshStatistics(item)) {
      console.log(`(skip) ${item.slug}`);
      continue;
    }

    console.log(`(fetch) ${item.slug}`);
    Object.assign(item, await getStatistics(item.slug));
  }
}

async function main() {
  const dictionary = await getDictionary();
  const current = await readTradeableItems();
  const tradeableItems = patchTradeableItems(dictionary, current);

  await updateStatistics(tradeableItems.data);
  await writeJson(TRADEABLE_ITEMS_PATH, tradeableItems);
}

await main();
