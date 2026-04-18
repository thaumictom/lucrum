import fs from 'node:fs';

const data = JSON.parse(fs.readFileSync('./data/market_statistics.json', 'utf8'));

const items = Array.isArray(data) ? data : data.item_statistics;

items
	.slice()
	.sort((a, b) => b.liquidity - a.liquidity)
	.slice(0, 100)
	.forEach(({ item, liquidity }, index) => {
		console.log(`${index + 1}. ${item} - ${liquidity}`);
	});
