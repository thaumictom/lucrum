import fs from 'node:fs';

const { item_statistics } = JSON.parse(
	fs.readFileSync('./data/warframe_market_statistics.json', 'utf8'),
);

item_statistics
	.sort((a, b) => (b.statistics_today?.median ?? 0) - (a.statistics_today?.median ?? 0))
	.slice(0, 100)
	.forEach(({ item, statistics_today }) => {
		console.log(`${item} - median: ${statistics_today?.median ?? 0}`);
	});
