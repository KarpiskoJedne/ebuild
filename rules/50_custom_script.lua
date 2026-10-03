-- rules/50_custom_script.lua — escape hatch: run an arbitrary command.
-- params: name, cmd (string, required), out (marker, required).
-- NOTE: cmd runs inside the daemon sandbox (loopback-only net, clean env).
-- Prefer a typed rule above; custom_script actions are flagged in SBOMs.
local M = {}
M.name = "custom_script"

function M.validate(p)
  if p.name == nil then error("custom_script: missing required param 'name'") end
  if p.cmd == nil then error("custom_script: missing required param 'cmd'") end
  if type(p.cmd) ~= "string" then error("custom_script: cmd must be a string") end
  if p.out == nil then error("custom_script: missing required param 'out'") end
  return true
end

function M.expand(p)
  M.validate(p)
  return { { rule = "gen", target = p.out, workdir = ".", argv = p.cmd } }
end

return M
