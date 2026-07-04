# Custom welcome card recipes

`shell3.welcome(str)` replaces the built-in splash with `str`, rendered
**verbatim** and centered — so it carries any ANSI the terminal understands
(16-color, 256-color, truecolor, box-drawing, ASCII art). It's evaluated once at
config load, and the config Lua VM has the full standard library, so the string
can be built however you like — including from a shell command.

## Inline, with color

Use `\27` for the escape byte (`string.char(27)` also works):

```lua
shell3.welcome(
  "\27[38;5;208m✦ my agent ✦\27[0m\n" ..
  "ready when you are"
)
```

## Dynamic — run a command in the body

The VM exposes `io.popen`, which runs its argument through `/bin/sh -c` (a full
shell: pipes, redirects, `$(...)`, globs). Read its output with `:read("*a")`.
No dedicated `welcome_cmd` option is needed — just call a command and use the
result:

```lua
-- cwd + git branch, colored, shown as the card
shell3.welcome(io.popen([[
  printf '\033[38;5;214m%s\033[0m\n' "$(pwd)"
  printf '\033[38;5;109mbranch: %s\033[0m\n' "$(git branch --show-current 2>/dev/null || echo none)"
]]):read("*a"))
```

Because it runs at load time, `pwd` is the directory shell3 was launched from;
the card refreshes the next time the config is loaded.

## ANSI art from a file

Keep elaborate art (from `figlet`, `toilet`, an image-to-ANSI converter, …) in a
file and `cat` it:

```lua
shell3.welcome(io.popen("cat " .. os.getenv("HOME") .. "/.shell3/lib/welcome.ansi"):read("*a"))
```

## Live palette test

A card that draws a truecolor gradient plus swatches of the terminal's own 16
ANSI colors — handy for checking how shell3 looks under a given color scheme:

```lua
local ESC, RESET = string.char(27), string.char(27) .. "[0m"
local function bg256(n) return ESC .. "[48;5;" .. n .. "m" end
local function bgrgb(r, g, b) return ESC .. "[48;2;" .. r .. ";" .. g .. ";" .. b .. "m" end

local bar = ""
for i = 0, 31 do
  local h = i / 32
  local r = math.floor(math.sin(2 * math.pi * (h + 0.0/3)) * 127 + 128)
  local g = math.floor(math.sin(2 * math.pi * (h + 1.0/3)) * 127 + 128)
  local b = math.floor(math.sin(2 * math.pi * (h + 2.0/3)) * 127 + 128)
  bar = bar .. bgrgb(r, g, b) .. " "
end
local function swatches(lo, hi)
  local s = ""
  for n = lo, hi do s = s .. bg256(n) .. "  " end
  return s .. RESET
end

shell3.welcome(table.concat({
  "", ESC .. "[1;38;5;214m๑ï  shell3" .. RESET, "",
  bar .. RESET, swatches(0, 7), swatches(8, 15), "",
}, "\n"))
```
