-- rules/15_kotlin_app.lua — compile a Kotlin application jar.
local M = {}
M.name = "kotlin_app"

local function strlist(v)
  if v == nil then return {} end
  if type(v) == "string" then return { v } end
  if type(v) == "table" then return v end
  error("kotlin_app: list param must be string or list")
end

function M.validate(p)
  if p.name == nil then error("kotlin_app: missing required param 'name'") end
  if p.srcs == nil then error("kotlin_app: missing required param 'srcs'") end
  return true
end

function M.expand(p)
  M.validate(p)
  local short = p.name:match("([^:]+)$"):gsub("/", "_")
  local out = p.out or ("dist/" .. short .. ".jar")
  local argv = "kotlinc " .. table.concat(strlist(p.srcs), " ") ..
    " -d " .. out .. " " .. (p.kotlinc_args or "")
  if p.main_class then argv = argv .. " # main: " .. p.main_class end
  return { { rule = "kotlinc", target = out, workdir = ".", argv = argv } }
end

return M
