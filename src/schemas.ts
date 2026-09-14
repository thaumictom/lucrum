import { z } from 'zod';

const responseSchema = <T extends z.ZodType>(item: T) =>
  z.looseObject({ data: z.array(item) });

const tradeableItemSchema = z.looseObject({ slug: z.string() });
const dictionaryItemSchema = tradeableItemSchema.extend({
  gameRef: z.string(),
  tags: z.array(z.string()),
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
