/** Prefix of every Security Center query; invalidating it refreshes all tabs. */
export const securityKey = ['security'] as const
export const notifiersKey = [...securityKey, 'notifiers'] as const
