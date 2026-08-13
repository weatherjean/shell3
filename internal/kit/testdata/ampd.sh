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
# agent: ampd-leads
# description: UK/IE WooCommerce lead-gen
# model: sonnet
# workdir: ~/ampd-leads
# use: [bash, web]
#---
ampd_prompt() { cat <<'EOF'
You find UK/IE indie e-commerce shops that need SEO help.
One tick = one niche. Judge each candidate yourself — a real indie shop, not
an agency or a marketplace. Write what you learned to memory.md.
EOF
}

#---
# tool: stack-check
# description: Classify a site's stack — wp_wc, shopify, wp_only, none
# params:
#   url:     {type: string, required: true, description: homepage URL}
#   timeout: {type: int, default: 20}
#---
ampd_stack_check() {
  local html
  html=$(curl -sL --max-time "$timeout" "$url") || return 1
  if   grep -q 'cdn\.shopify\.com'     <<<"$html"; then echo shopify
  elif grep -q '/plugins/woocommerce/' <<<"$html"; then echo wp_wc
  elif grep -q 'wp-content'            <<<"$html"; then echo wp_only
  else echo none; fi
}

#---
# test: stack-check — classifies each stack
#---
ampd_test_stack_check() {
  stub curl <<<'<link href="/plugins/woocommerce/x.css">'
  assert_eq "$(tool stack-check url=https://x.test)" wp_wc

  stub curl <<<'<html>just a blog</html>'
  assert_eq "$(tool stack-check url=https://x.test)" none
}

#---
# skill: qualify
#---
ampd_skill_qualify() { cat <<'EOF'
A real shop has products with prices, a cart, a company name in the footer.
An agency has case studies, a services nav, no cart. Reject agencies,
directories, marketplaces, and anything whose newest product is >2y old.
EOF
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
