-- rules/47_vite_build.lua — production build via Vite.
-- params: name, config(opt), out(opt marker dir), sourcemap(bool,opt).
local M = {}
M.name = "vite_build"

function M.validate(p)
  if p.name == nil then error("vite_build: missing required param 'name'") end
  return true
end

function M.expand(p)
  M.validate(p)
  local short = p.name:match("([^:]+)$"):gsub("/", "_")
  local out = p.out or ("dist/" .. short .. "/.vite-ok")
  local argv = "vite build"
  if p.config then argv = argv .. " --config " .. p.config end
  if p.sourcemap then argv = argv .. " --sourcemap" end
  return { { rule = "node", target = out, workdir = ".",
             argv = argv .. " && touch " .. out } }
end

return M
