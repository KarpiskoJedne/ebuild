-- rules/30_csharp_assembly.lua — build a .NET assembly.
-- params: name, project(opt, default "."), configuration(opt), out(opt).
local M = {}
M.name = "csharp_assembly"

function M.validate(p)
  if p.name == nil then error("csharp_assembly: missing required param 'name'") end
  return true
end

function M.expand(p)
  M.validate(p)
  local cfg = p.configuration or "Release"
  local argv = "dotnet build " .. (p.project or ".") .. " -c " .. cfg .. " --nologo"
  local short = p.name:match("([^:]+)$"):gsub("/", "_")
  local out = p.out or (".ebuild/bin/" .. cfg .. "/" .. short .. ".dll")
  return { { rule = "csc", target = out, workdir = ".", argv = argv } }
end

return M
