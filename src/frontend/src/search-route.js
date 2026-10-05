// @ts-check

/** @param {string} value */
export function effectiveSearchQuery(value) {
  return value.trim();
}

/** @param {string} search */
export function searchQueryFromSearch(search) {
  const params = new URLSearchParams(search);
  return effectiveSearchQuery(params.get("q") ?? "");
}

/**
 * Build the canonical board URL for an existing pathname without interpreting
 * the search grammar. The backend remains the sole parser for q.
 *
 * @param {string} pathname
 * @param {string} query
 * @param {boolean} [benchmark]
 */
export function boardURL(pathname, query, benchmark = false) {
  const params = new URLSearchParams();
  const effective = effectiveSearchQuery(query);
  if (effective !== "") params.set("q", effective);
  if (benchmark) params.set("bench", "");
  const encoded = params.toString();
  return encoded === "" ? pathname : `${pathname}?${encoded}`;
}
