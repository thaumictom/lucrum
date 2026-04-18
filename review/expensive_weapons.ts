import { readFileSync } from 'node:fs';

const dict = JSON.parse(readFileSync('./data/warframe_dictionary.json', 'utf8'));
const stats = JSON.parse(readFileSync('./data/market_statistics.json', 'utf8'));

const statsBySlug = new Map(
	stats.item_statistics.map((x: any) => [
		x.item,
		{ price: x.statistics_today?.median ?? 0, liquidity: x.liquidity ?? 0 },
	]),
);

dict.items
	.filter((x: any) => x.tags.includes('warframe') && x.tags.includes('set'))
	.map((x: any) => ({
		slug: x.slug,
		name: x.name,
		price: statsBySlug.get(x.slug)?.price ?? 0,
		liquidity: statsBySlug.get(x.slug)?.liquidity ?? 0,
	}))
	.filter((x: any) => x.liquidity > 0)
	.sort((a: any, b: any) => b.price - a.price)
	.forEach((x: any) =>
		console.log(`${x.name} (${x.slug}) - ${x.price} | liquidity: ${x.liquidity}`),
	);
