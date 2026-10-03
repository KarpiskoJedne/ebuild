-- rules/40_julia_script.lua — run a Julia script / instantiate a project.
-- params: name, script, project(opt), args(list,opt).
local M = {}
M.name = "julia_script"

local function strlist(v)
  if v == nil then return {} end
  if type(v) == "string" then return { v } end
  if type(v) == "table" then return v end
  error("julia_script: list param must be string or list")
end

function M.validate(p)
  if p.name == nil then error("julia_script: missing required param 'name'") end
  if p.script == nil then error("julia_script: missing required param 'script'") end
  if not p.script:match("%.jl$") then error("julia_script: script must be a .jl file") end
  return true
end

function M.expand(p)
  M.validate(p)
  local argv = "julia"
  if p.project then argv = argv .. " --project=" .. p.project end
  argv = argv .. " " .. p.script .. " " .. table.concat(strlist(p.args), " ")
  local short = p.name:match("([^:]+)$"):gsub("/", "_")
  return { { rule = "gen", target = ".ebuild/julia/" .. short .. ".ok",
             workdir = ".", argv = argv } }
end

return M
