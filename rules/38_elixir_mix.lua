-- rules/38_elixir_mix.lua — compile/test an Elixir Mix project.
-- params: name, dir(opt, default "."), task("compile"|"test"|"release").
local M = {}
M.name = "elixir_mix"

function M.validate(p)
  if p.name == nil then error("elixir_mix: missing required param 'name'") end
  local t = p.task or "compile"
  if t ~= "compile" and t ~= "test" and t ~= "release" then
    error("elixir_mix: task must be compile|test|release")
  end
  return true
end

function M.expand(p)
  M.validate(p)
  local dir = p.dir or "."
  local t = p.task or "compile"
  local argv = "mix " .. t
  if t == "release" then argv = "mix release --overwrite" end
  local short = p.name:match("([^:]+)$"):gsub("/", "_")
  local argv_full = "sh -c 'cd " .. dir .. " && " .. argv .. "'"
  return { { rule = "gen", target = ".ebuild/mix/" .. short .. "." .. t .. ".ok",
             workdir = ".", argv = argv_full } }
end

return M
