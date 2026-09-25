// Central query-key factory — every useQuery/useMutation invalidation in the
// app should import from here instead of retyping a string array, so a key
// shape only ever needs to change in one place.
export const queryKeys = {
  gatewayHealth: () => ["gateway-health"] as const,
  categories: () => ["categories"] as const,
  storefrontProducts: (categoryId: string | null, page: number, q?: string, vendorId?: string) =>
    ["storefront-products", categoryId, page, q ?? null, vendorId ?? null] as const,
  // viewerId keeps the cache correct across login/logout on the same page:
  // the response can differ per viewer (see useProduct/getProductBySlug —
  // an admin or the product's own vendor gets exact stock_quantity back).
  product: (slug: string, viewerId: string | null) => ["product", slug, viewerId] as const,
  cart: () => ["cart"] as const,
  buyerAddresses: () => ["buyer-addresses"] as const,
  ordersMine: (page?: number) =>
    page === undefined ? (["orders-mine"] as const) : (["orders-mine", page] as const),
  order: (id: string) => ["order", id] as const,
  paymentIntent: (id: string) => ["payment-intent", id] as const,
  myShipments: () => ["my-shipments"] as const,
  shipmentEvents: (shipmentId: string) => ["shipment-events", shipmentId] as const,
} as const;
