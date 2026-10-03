-- rules/33_haskell_package.lua — build a Haskell package via cabal.
-- params: name, package(opt, default "."), tests(bool,opt), out(opt marker).
local M = {}
M.name = "haskell_package"

function M.validate(p)
  if p.name == nil then error("haskell_package: missing required param 'name'") end
  return true
end

function M.expand(p)
  M.validate(p)
  local pkg = p.package or "."
  local argv = "cabal build " .. pkg
  if p.tests then argv = argv .. " && cabal test " .. pkg end
  local short = p.name:match("([^:]+)$"):gsub("/", "_")
  local out = p.out or (".ebuild/haskell/" .. short .. ".ok")
  return { { rule = "gen", target = out, workdir = ".", argv = argv .. " && touch " .. out } }
end

return M
