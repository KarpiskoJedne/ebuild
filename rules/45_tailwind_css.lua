-- rules/45_tailwind_css.lua — build minified CSS via the Tailwind CLI.
-- params: name, input, out(opt), minify(bool,opt, default true).
local M = {}
M.name = "tailwind_css"

function M.validate(p)
  if p.name == nil then error("tailwind_css: missing required param 'name'") end
  if p.input == nil then error("tailwind_css: missing required param 'input'") end
  if not p.input:match("%.css$") then error("tailwind_css: input must be .css") end
  return true
end

function M.expand(p)
  M.validate(p)
  local short = p.name:match("([^:]+)$"):gsub("/", "_")
  local out = p.out or ("dist/" .. short .. ".css")
  local argv = "tailwindcss -i " .. p.input .. " -o " .. out
  if p.minify ~= false then argv = argv .. " --minify" end
  return { { rule = "gen", target = out, workdir = ".", argv = argv } }
end

return M
