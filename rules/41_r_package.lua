-- rules/41_r_package.lua — check + install an R package.
-- params: name, dir(opt, default "."), check(bool,opt, default true).
local M = {}
M.name = "r_package"

function M.validate(p)
  if p.name == nil then error("r_package: missing required param 'name'") end
  return true
end

function M.expand(p)
  M.validate(p)
  local dir = p.dir or "."
  local argv = "R CMD INSTALL --library=.ebuild/R " .. dir
  if p.check ~= false then argv = "R CMD check --no-manual " .. dir .. " && " .. argv end
  local short = p.name:match("([^:]+)$"):gsub("/", "_")
  return { { rule = "rlang", target = ".ebuild/R/" .. short .. ".ok",
             workdir = ".", argv = argv } }
end

return M
