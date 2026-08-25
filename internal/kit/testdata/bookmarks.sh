#---
# shell3:
#   background: {max_concurrent: 8}
#---

#---
# agent: main
# model: opus
# use: [web]
#---
main_prompt() { cat <<'EOF'
You are the agent I talk to. You do work directly, or dispatch an employee.
EOF
}

#---
# agent: bookmarks
# description: keeps my saved links tidy
# model: sonnet
# workdir: ~/bookmarks
# use: [bash, web]
#---
bm_prompt() { cat <<'EOF'
You check the links I saved and file the ones still worth reading.
One tick = one batch. Judge each page yourself — something I would actually
open again, not a parked domain. Write what you learned to memory.md.
EOF
}

#---
# tool: page-kind
# description: Classify a saved link — article, wiki, shop, dead
# params:
#   url:     {type: string, required: true, description: page URL}
#   timeout: {type: int, default: 20}
#---
bm_page_kind() {
  local html
  html=$(curl -sL --max-time "$timeout" "$url") || return 1
  if   grep -q 'id="mw-content-text"' <<<"$html"; then echo wiki
  elif grep -q 'add-to-cart'          <<<"$html"; then echo shop
  elif grep -q '<article'             <<<"$html"; then echo article
  else echo dead; fi
}

#---
# test: page-kind — classifies each kind
#---
bm_test_page_kind() {
  stub curl <<<'<article><h1>a post</h1></article>'
  assert_eq "$(tool page-kind url=https://x.test)" article

  stub curl <<<'<html>domain for sale</html>'
  assert_eq "$(tool page-kind url=https://x.test)" dead
}

#---
# shared: web
#---
#---
# tool: search
# description: Search the web via SearXNG
# params:
#   q:    {type: string, required: true}
#   lang: {type: string, default: en-GB}
#---
web_search() { searx_query "$q" "$lang"; }
