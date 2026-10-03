-- rules/39_erlang_otp.lua — compile an Erlang/OTP application via rebar3.
-- params: name, dir(opt, default "."), profile(opt, default "default").
local M = {}
M.name = "erlang_otp"

function M.validate(p)
  if p.name == nil then error("erlang_otp: missing required param 'name'") end
  return true
end

function M.expand(p)
  M.validate(p)
  local dir = p.dir or "."
  local profile = p.profile or "default"
  local argv = "sh -c 'cd " .. dir .. " && rebar3 as " .. profile .. " compile'"
  local short = p.name:match("([^:]+)$"):gsub("/", "_")
  return { { rule = "gen", target = ".ebuild/otp/" .. short .. ".ok",
             workdir = ".", argv = argv } }
end

return M
