import test from "node:test";
import assert from "node:assert/strict";

import { boardURL, effectiveSearchQuery, searchQueryFromSearch } from "./search-route.js";

test("search query is represented canonically without frontend parsing", () => {
  assert.equal(effectiveSearchQuery("  cat -anime score:>=100  "), "cat -anime score:>=100");
  assert.equal(
    boardURL("/post/42", "  cat -anime score:>=100  "),
    "/post/42?q=cat+-anime+score%3A%3E%3D100",
  );
  assert.equal(boardURL("/", "   "), "/");
});

test("search query round-trips through browser search state", () => {
  assert.equal(searchQueryFromSearch("?q=%20cat+-anime%20&bench="), "cat -anime");
  assert.equal(searchQueryFromSearch("?bench="), "");
  assert.equal(boardURL("/", "tag one", true), "/?q=tag+one&bench=");
});
