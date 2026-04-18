import fs from 'node:fs';

const { item_statistics } = JSON.parse(fs.readFileSync('./data/market_statistics.json', 'utf8'));

item_statistics
	.filter((x) => x.item.endsWith('relic') && x.liquidity > 5)
	.sort((a, b) => b.statistics_today.median - a.statistics_today.median)
	.forEach(({ item, liquidity, statistics_today }) => {
		console.log(`${item} - median: ${statistics_today.median}, liquidity: ${liquidity}`);
	});
