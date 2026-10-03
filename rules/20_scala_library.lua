-- rules/20_scala_library.lua — compile a Scala library jar.
local M = {}
M.name = "scala_library"

local function strlist(v)
  if v == nil then return {} end
  if type(v) == "string" then return { v } end
  if type(v) == "table" then return v end
  error("scala_library: list param must be string or list")
end

function M.validate(p)
  if p.name == nil then error("scala_library: missing required param 'name'") end
  if p.srcs == nil then error("scala_library: missing required param 'srcs'") end
  return true
end

function M.expand(p)
  M.validate(p)
  local short = p.name:match("([^:]+)$"):gsub("/", "_")
  local classes = ".ebuild/scala/" .. short
  local out = p.out or ("dist/" .. short .. ".jar")
  local argv = "scalac -d " .. classes .. " " .. (p.scalac_args or "") .. " " ..
    table.concat(strlist(p.srcs), " ") ..
    " && jar --create --file " .. out .. " -C " .. classes .. " ."
  return { { rule = "gen", target = out, workdir = ".", argv = argv } }
end

return M
