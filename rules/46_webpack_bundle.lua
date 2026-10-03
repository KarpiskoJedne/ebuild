-- rules/46_webpack_bundle.lua — production bundle via webpack.
-- params: name, config(opt, default "webpack.config.js"), mode(opt), out(opt).
local M = {}
M.name = "webpack_bundle"

function M.validate(p)
  if p.name == nil then error("webpack_bundle: missing required param 'name'") end
  local m = p.mode or "production"
  if m ~= "production" and m ~= "development" and m ~= "none" then
    error("webpack_bundle: bad mode '" .. m .. "'")
  end
  return true
end

function M.expand(p)
  M.validate(p)
  local short = p.name:match("([^:]+)$"):gsub("/", "_")
  local out = p.out or ("dist/" .. short .. ".bundle.js")
  local argv = "webpack --config " .. (p.config or "webpack.config.js") ..
    " --mode " .. (p.mode or "production")
  return { { rule = "node", target = out, workdir = ".", argv = argv } }
end

return M
