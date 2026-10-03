-- rules/16_kotlin_library.lua — compile a Kotlin library jar.
local M = {}
M.name = "kotlin_library"

local function strlist(v)
  if v == nil then return {} end
  if type(v) == "string" then return { v } end
  if type(v) == "table" then return v end
  error("kotlin_library: list param must be string or list")
end

function M.validate(p)
  if p.name == nil then error("kotlin_library: missing required param 'name'") end
  if p.srcs == nil then error("kotlin_library: missing required param 'srcs'") end
  return true
end

function M.expand(p)
  M.validate(p)
  local short = p.name:match("([^:]+)$"):gsub("/", "_")
  local out = p.out or ("dist/" .. short .. ".jar")
  local argv = "kotlinc -no-stdlib " .. table.concat(strlist(p.srcs), " ") ..
    " -d " .. out .. " " .. (p.kotlinc_args or "")
  return { { rule = "kotlinc", target = out, workdir = ".", argv = argv } }
end

return M
