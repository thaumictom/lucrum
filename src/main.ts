import {
	dictionarySchema,
	environmentSchema,
	itemsSchema,
	statisticsSchema,
	tradeableItemsSchema,
} from './schemas';

const paths = {
	dictionary: 'data/dictionary.json',
	tradeableItems: 'data/tradeable_items.json',
};
const wfmUrl = 'https://api.warframe.market';
const cacheLifetime = 4 * 60 * 60 * 1000;
const environment = environmentSchema.parse(Bun.env);
let lastRequest = 0;

async function request(path: string) {
	const wait = 1000 / environment.LUCRUM_REQUESTS_PER_SECOND - (Date.now() - lastRequest);
	if (wait > 0) await Bun.sleep(wait);
	lastRequest = Date.now();
	const fetched_at = new Date().toISOString();

	const response = await fetch(`${wfmUrl}${path}`, { signal: AbortSignal.timeout(60_000) });
	if (!response.ok) throw new Error(`${path}: ${response.status} ${response.statusText}`);
	return { response, fetched_at };
}

function utcDate(daysAgo: number) {
	const date = new Date();
	date.setUTCDate(date.getUTCDate() - daysAgo);
	return date.toISOString().slice(0, 10);
}

async function getStatistics(slug: string) {
	const { response, fetched_at } = await request(`/v1/items/${slug}/statistics`);
	const rows = statisticsSchema.parse(await response.json()).payload.statistics_closed['90days'];
	const select = (daysAgo: number) =>
		rows
			.filter(({ datetime }) => new Date(datetime).toISOString().startsWith(utcDate(daysAgo)))
			.map(({ datetime, id, order_type, ...row }) => row);
	const statistics_yesterday = select(2);
	const statistics_today = select(1);

	return {
		fetched_at,
		statistics_yesterday,
		statistics_today,
		liquidity: [...statistics_yesterday, ...statistics_today].reduce(
			(sum, { volume }) => sum + volume,
			0,
		),
	};
}

function hasFreshStatistics(item: { fetched_at?: string; liquidity?: number }) {
	if (item.fetched_at === undefined || item.liquidity === undefined) return false;
	const hours = item.liquidity <= 20 ? 24 : item.liquidity >= 21 && item.liquidity <= 150 ? 6 : 0;
	return hours > 0 && Date.now() - Date.parse(item.fetched_at) <= hours * 60 * 60 * 1000;
}

async function getDictionary() {
	const file = Bun.file(paths.dictionary);
	if (await file.exists()) {
		const cached = dictionarySchema.safeParse(await file.json());
		if (cached.success && Date.now() - Date.parse(cached.data.fetched_at) <= cacheLifetime)
			return cached.data;
	}

	const { response, fetched_at } = await request('/v2/items');
	const responseBody = itemsSchema.parse(await response.json());
	const dictionary = {
		...responseBody,
		fetched_at,
		data: responseBody.data.map(({ id, i18n, ...item }) => ({
			...item,
			name: i18n.en.name,
		})),
	};

	await Bun.write(paths.dictionary, JSON.stringify(dictionary, null, 2));
	return dictionary;
}

async function updateTradeableItems() {
	const dictionary = await getDictionary();
	const file = Bun.file(paths.tradeableItems);
	const current = (await file.exists())
		? tradeableItemsSchema.parse(await file.json())
		: { data: [] };
	const currentBySlug = new Map(current.data.map((item) => [item.slug, item]));

	const tradeableItems = {
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
	const { LUCRUM_ITEM_OFFSET, LUCRUM_ITEM_LIMIT } = environment;
	const items = tradeableItems.data.slice(
		LUCRUM_ITEM_OFFSET,
		LUCRUM_ITEM_LIMIT ? LUCRUM_ITEM_OFFSET + LUCRUM_ITEM_LIMIT : undefined,
	);

	for (const item of items) {
		if (hasFreshStatistics(item)) continue;
		Object.assign(item, await getStatistics(item.slug));
	}

	await Bun.write(paths.tradeableItems, JSON.stringify(tradeableItems, null, 2));
}

await updateTradeableItems();
