-- rules/28_ruby_gem.lua — build a Ruby gem from a gemspec.
-- params: name, gemspec, out(opt).
local M = {}
M.name = "ruby_gem"

function M.validate(p)
  if p.name == nil then error("ruby_gem: missing required param 'name'") end
  if p.gemspec == nil then error("ruby_gem: missing required param 'gemspec'") end
  if not p.gemspec:match("%.gemspec$") then
    error("ruby_gem: gemspec must end in .gemspec")
  end
  return true
end

function M.expand(p)
  M.validate(p)
  local short = p.name:match("([^:]+)$"):gsub("/", "_")
  local out = p.out or ("dist/" .. short .. ".gem")
  local argv = "gem build " .. p.gemspec .. " --output " .. out
  return { { rule = "gen", target = out, workdir = ".", argv = argv } }
end

return M
