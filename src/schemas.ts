import { z } from 'zod';

const responseSchema = <T extends z.ZodType>(item: T) =>
  z.looseObject({ data: z.array(item) });

const itemSchema = z.looseObject({ slug: z.string() });
const dictionaryItemSchema = itemSchema.extend({
  gameRef: z.string(),
  tags: z.array(z.string()),
});
const tradeableItemSchema = itemSchema.extend({
  fetched_at: z.iso.datetime().optional(),
  liquidity: z.number().optional(),
});
const apiItemSchema = dictionaryItemSchema.extend({
  id: z.string(),
  i18n: z.looseObject({
    en: z.looseObject({ name: z.string() }),
  }),
});

export const itemsSchema = responseSchema(apiItemSchema);
export const dictionarySchema = responseSchema(dictionaryItemSchema).extend({
  fetched_at: z.iso.datetime(),
});
export const tradeableItemsSchema = responseSchema(tradeableItemSchema);

const statisticSchema = z.looseObject({
  datetime: z.iso.datetime({ offset: true }),
  id: z.string(),
  volume: z.number(),
  order_type: z.string().optional(),
});

export const statisticsSchema = z.looseObject({
  payload: z.looseObject({
    statistics_closed: z.looseObject({
      '90days': z.array(statisticSchema),
    }),
  }),
});

export const environmentSchema = z.object({
  LUCRUM_REQUESTS_PER_SECOND: z.coerce.number().positive().max(3).default(2.5),
  LUCRUM_ITEM_OFFSET: z.coerce.number().int().nonnegative().default(0),
  LUCRUM_ITEM_LIMIT: z.coerce.number().int().nonnegative().default(0),
});

export type Dictionary = z.infer<typeof dictionarySchema>;
export type Statistic = z.infer<typeof statisticSchema>;
export type TradeableItems = z.infer<typeof tradeableItemsSchema>;
