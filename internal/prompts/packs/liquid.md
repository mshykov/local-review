# Liquid review pack (Shopify)

Apply the default review rules. Plus: Liquid / Shopify-theme patterns to look for.

Liquid is a template language, not a programming language — review priorities skew toward **output safety (XSS), performance (storefront page weight), theme conventions, and accessibility**. Logic bugs do happen but are far less common than the four above.

## High-signal Liquid pitfalls

### Output safety (XSS)
- **Unescaped user-controlled output** — Liquid auto-escapes by default in Shopify Online Store 2.0 themes, but **only inside `{{ }}` HTML contexts**. Output placed in `<script>`, `<style>`, attribute values without quotes, `href="…"`/`src="…"` URL contexts, or inline event handlers needs explicit `| escape` or `| json` (for JS contexts) — auto-escape does NOT cover those.
- **`| raw` filter on customer-supplied content** — explicitly disables escaping; flag any `| raw` on `product.metafields.*`, `customer.*`, `cart.attributes.*`, search-query strings, or anything reachable from URL params.
- **JS embedding via `{{ }}`** without `| json` — `<script>var name = "{{ product.title }}";</script>` breaks (and is XSS-able) when the title contains `"` or `</script>`. Always `| json` for JS literal injection.
- **`url`-context output** without `| url_encode` — query-string params that contain `&`, `=`, or `#` break the URL and can be used to redirect or inject.
- **`| escape_once` vs `| escape`** — `escape_once` won't double-escape already-encoded entities, which usually means you got the input from somewhere already-encoded; double-check the chain.
- **Inline event handlers** (`onclick="doThing('{{ x }}')"`) — multiple escape contexts at once (HTML attribute + JS string); almost always the wrong shape, use a `data-*` attribute + external JS.
- **`canonical_url`, `og:url`, `link` tags built from `request.path`** without sanitizing — open-redirect / phishing surface.

### Performance (page weight + render time)
- **`{% for ... in collections.all.products %}`** without a `limit:` — iterates every product in the shop; will time out on shops with thousands of SKUs (Shopify caps theme rendering).
- **Nested `{% for %}` over collections.products** — quadratic; almost always a sign the logic belongs in a query / metafield rather than the template.
- **`paginate` not used on large listings** — without `{% paginate collection.products by 24 %}` the page renders all products at once.
- **`{% assign %}` of a heavy filter chain inside a loop** — recomputed every iteration; lift out of the loop.
- **`| where: 'tag', 'x'`** applied repeatedly to the same collection inside a loop — same recomputation problem.
- **Image URLs without `| image_url: width: N`** or without `width=`/`height=` attributes — serves full-size assets, blocks LCP. Use the responsive image filter or `media_url` with explicit size.
- **`{% render 'snippet' %}` (or worse, `{% include %}`) inside a hot loop** — each render is a full snippet execution; if it doesn't depend on the loop variable, hoist it.
- **`section.blocks` iteration** without a `case` on block.type — the all-blocks path runs every renderer even when only one type is present.
- **Inline `<style>` / `<script>` blocks added per item** — duplicated CSS/JS in every product card; should live in the theme CSS bundle.
- **JavaScript-heavy `{{ shop.javascript }}` / `content_for_header` overrides** — a custom script in the header blocks render; defer / async unless it's truly critical.

### Liquid logic correctness
- **`{% if x %}` where `x` is a string** — empty string is truthy in Liquid (it's a defined value), unlike most languages. Use `{% if x != blank %}` or `{% if x != '' %}` explicitly.
- **`nil` vs `blank`** — `nil` is "the variable was never set"; `blank` matches `nil`, empty string, and empty arrays. `{% if x == blank %}` is usually what you want for "no useful value."
- **`{% unless x %}` with a chained condition** — `{% unless x and y %}` is De Morgan's nightmare ("unless both" reads as "if either is not"). Liquid has no `not` unary operator and doesn't support parentheses for grouping, so refactor by either splitting into nested `{% if %}` blocks (`{% if x %}{% if y %}…{% endif %}{% endif %}`) or rewriting as the comparison-level complement (`{% if x == blank or y == blank %}` for the "either is missing" case, depending on the underlying intent).
- **`{% assign x = ... %}` inside `{% for %}`** intending closure capture — Liquid `assign` is global to the scope, not a fresh binding per iteration.
- **`{% capture %}` with no end tag** — silent breakage; the rest of the template captures into `x` and renders nothing.
- **`forloop.index` vs `forloop.index0`** — off-by-one source. `index` starts at 1; `index0` starts at 0.
- **`{% break %}` / `{% continue %}` inside nested loops** — only break the innermost; reach for `forloop.last`/`forloop.first` when you need the outer behavior.
- **Filter applied to `nil`** — most filters return the empty string or `0` silently; the resulting template render is "missing data" with no error. `{{ undefined_var | default: 'fallback' }}` is the safe shape.

### Shopify-specific conventions
- **Hardcoded English copy** in a theme that supports translations — should be `{{ 'section.key' | t }}` with a key in `locales/en.default.json`.
- **Money output without `| money`** filter — raw cents-based integers leak (`299` instead of `$2.99`).
- **`product.price` displayed where `product.price_min` … `product.price_max`** is meaningful — single-variant assumption hides variant range.
- **`customer.first_name` shown when `customer == nil`** — guard with `{% if customer %}`.
- **`product.available`** vs **`product.selected_or_first_available_variant.available`** — the former is "any variant is in stock," the latter is "the currently-shown variant is in stock." Mixing them up shows "in stock" while the selected variant is sold out.
- **`section.settings.*` accessed without a default** — settings the merchant hasn't configured render as `blank`, which often means a broken-looking page on a fresh install.
- **`{% schema %}` block missing or malformed** — section won't appear in the theme editor.
- **Hardcoded image asset paths** instead of `{{ 'image.png' | asset_url }}` — breaks on theme rename / CDN re-deploy.
- **`{% form 'product' %}` without the matching `{% endform %}`** — silently broken cart-add.
- **Custom App-block contracts** — Shopify App Blocks have specific schema requirements; missing fields fail validation only on theme save, not at template render.

### Accessibility
- **`<img>` without `alt`** — Liquid templates often build images from product/asset data; `alt="{{ image.alt | default: product.title }}"` or empty `alt=""` for decorative.
- **`<a>` with no accessible name** — link with only an icon glyph; needs an `aria-label` or visually-hidden text.
- **Form inputs without `<label>`** — `placeholder=` is not a label substitute.
- **Color contrast** in inline styles built from theme settings — `style="color: {{ section.settings.text_color }}"` can produce unreadable text against `{{ section.settings.bg_color }}` when both are merchant-configurable.

### Security beyond XSS
- **`request.referrer`, `request.host` used as trust signals** — client-controlled headers, do not gate logic on them.
- **`customer.id` / `customer.email` exposed in JS for analytics** without thinking about PII leakage to third parties.
- **`{{ canonical_url }}` reused as a redirect target** — open redirect.
- **API tokens / app-proxy secrets** in template files — never; should be in app metafields/private metafields read server-side.

## Idioms & style

- **Snake_case filter names** — Liquid filter convention (`image_url`, `where`, `map`, `compact`, `escape`).
- **One filter per pipe** — chained filters read top-to-bottom; long chains across one line are unreadable, prefer multi-line.
- **`{% liquid %}` block for multiple statements** — Shopify-specific shorthand that drops the `{%` and `%}` delimiters around each statement inside the block; cleaner for long logic sections.
- **`{% render %}` over `{% include %}`** — `include` is deprecated; `render` has explicit variable scoping (the snippet sees only what you pass).
- **`{% comment %} … {% endcomment %}`** for multi-line; `{# … #}` (Jekyll) does NOT work in Liquid.
- **Empty-state guards at the top** — `{% if products.size == 0 %}…{% else %}{% for %}…{% endfor %}{% endif %}` reads cleaner than testing inside the loop body.

