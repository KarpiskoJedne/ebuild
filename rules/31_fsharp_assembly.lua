-- rules/31_fsharp_assembly.lua — build an F# assembly via dotnet.
local M = {}
M.name = "fsharp_assembly"

function M.validate(p)
  if p.name == nil then error("fsharp_assembly: missing required param 'name'") end
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
