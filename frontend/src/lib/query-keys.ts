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
  checkoutPreviewAll: () => ["checkout-preview"] as const,
  checkoutPreview: (addressId: string, cartVersion: number) =>
    ["checkout-preview", addressId, cartVersion] as const,
  vendorReturns: (vendorId: string, status: string) =>
    ["vendor-returns", vendorId, status] as const,
  paymentIntent: (id: string) => ["payment-intent", id] as const,
  myShipments: () => ["my-shipments"] as const,
  shipmentEvents: (shipmentId: string) => ["shipment-events", shipmentId] as const,
  supportCapability: () => ["support-capability"] as const,
  // scope + viewer: a buyer, a shop and an admin see different fields.
  supportCasesAll: () => ["support-cases"] as const,
  supportCases: (scope: string, filters: Record<string, string | boolean | undefined>) =>
    ["support-cases", scope, filters] as const,
  supportCase: (scope: string, caseId: string) => ["support-case", scope, caseId] as const,
  // AF-17: shops the signed-in person may open, with capabilities.
  accessibleShops: () => ["accessible-shops"] as const,
  memberShop: (vendorId: string) => ["member-shop", vendorId] as const,
  shopMembers: (vendorId: string) => ["shop-members", vendorId] as const,
  staffInvitations: (vendorId: string) => ["staff-invitations", vendorId] as const,
  staffPermissions: () => ["staff-permissions"] as const,
  // AF-19: the admin's bundles, access screen and approval queue.
  adminPermissions: (userId: string) => ["admin-permissions", userId] as const,
  permissionSubjects: () => ["permission-subjects"] as const,
  approvalRequests: (status: string) => ["approval-requests", status] as const,
  // AF-06: the buyer's refunds of one order and an admin's manual transfer view.
  myRefunds: (orderId: string) => ["my-refunds", orderId] as const,
  manualRefund: (refundId: string) => ["manual-refund", refundId] as const,
  // AF-09: the signed-in person's inbox (cleared on logout with the cache).
  inboxAll: () => ["inbox"] as const,
  inboxUnread: (userId: string) => ["inbox", "unread", userId] as const,
  inboxPages: (userId: string, unreadOnly: boolean) =>
    ["inbox", "pages", userId, unreadOnly] as const,
  inboxPreview: (userId: string) => ["inbox", "preview", userId] as const,
  notificationPreferences: (userId: string) => ["notification-preferences", userId] as const,
} as const;
