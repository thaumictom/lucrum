import { dictionarySchema, itemsSchema, tradeableItemsSchema } from './schemas';

const paths = {
	dictionary: 'data/dictionary.json',
	tradeableItems: 'data/tradeable_items.json',
};
const wfmUrl = 'https://api.warframe.market';
const cacheLifetime = 4 * 60 * 60 * 1000;

async function getDictionary() {
	const file = Bun.file(paths.dictionary);
	if (await file.exists()) {
		const cached = dictionarySchema.safeParse(await file.json());
		if (cached.success && Date.now() - Date.parse(cached.data.fetched_at) <= cacheLifetime)
			return cached.data;
	}

	const response = await fetch(`${wfmUrl}/v2/items`);
	if (!response.ok) throw new Error(`${response.status} ${response.statusText}`);

	const responseBody = itemsSchema.parse(await response.json());
	const dictionary = {
		...responseBody,
		fetched_at: new Date().toISOString(),
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

	await Bun.write(paths.tradeableItems, JSON.stringify(tradeableItems, null, 2));
}

await updateTradeableItems();
