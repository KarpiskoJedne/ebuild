-- rules/32_clojure_project.lua — build/test a Clojure project via clojure CLI.
-- params: name, aliases(list,opt), task("uberjar"|"test", default "uberjar").
local M = {}
M.name = "clojure_project"

local function strlist(v)
  if v == nil then return {} end
  if type(v) == "string" then return { v } end
  if type(v) == "table" then return v end
  error("clojure_project: list param must be string or list")
end

function M.validate(p)
  if p.name == nil then error("clojure_project: missing required param 'name'") end
  local t = p.task or "uberjar"
  if t ~= "uberjar" and t ~= "test" then
    error("clojure_project: task must be uberjar|test")
  end
  return true
end

function M.expand(p)
  M.validate(p)
  local aliases = strlist(p.aliases)
  local aflags = ""
  if #aliases > 0 then aflags = "-M:" .. table.concat(aliases, ":") .. " " end
  local argv
  if (p.task or "uberjar") == "test" then
    argv = "clojure " .. aflags .. "-m cognitect.test-runner"
  else
    argv = "clojure " .. aflags .. "-T:build uber"
  end
  local short = p.name:match("([^:]+)$"):gsub("/", "_")
  local out = p.out or ("target/" .. short .. ".jar")
  return { { rule = "gen", target = out, workdir = ".", argv = argv } }
end

return M
