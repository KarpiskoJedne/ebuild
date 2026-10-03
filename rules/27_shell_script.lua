-- rules/27_shell_script.lua — shellcheck + execute a shell script target.
-- params: name, script, args(list,opt), shell(opt, default "sh").
local M = {}
M.name = "shell_script"

local function strlist(v)
  if v == nil then return {} end
  if type(v) == "string" then return { v } end
  if type(v) == "table" then return v end
  error("shell_script: list param must be string or list")
end

function M.validate(p)
  if p.name == nil then error("shell_script: missing required param 'name'") end
  if p.script == nil then error("shell_script: missing required param 'script'") end
  local sh = p.shell or "sh"
  if sh ~= "sh" and sh ~= "bash" and sh ~= "dash" and sh ~= "zsh" then
    error("shell_script: unsupported shell '" .. sh .. "'")
  end
  return true
end

function M.expand(p)
  M.validate(p)
  local sh = p.shell or "sh"
  local argv = "shellcheck -S warning " .. p.script .. " && " .. sh ..
    " " .. p.script .. " " .. table.concat(strlist(p.args), " ")
  local short = p.name:match("([^:]+)$"):gsub("/", "_")
  return { { rule = "gen", target = ".ebuild/sh/" .. short .. ".ok",
             workdir = ".", argv = argv } }
end

return M
