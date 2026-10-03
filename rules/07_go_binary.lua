-- rules/07_go_binary.lua — build a Go binary.
-- params: name, package(opt, default "."), ldflags(opt), tags(list,opt),
--         out(opt), cgo(bool,opt, default false).
local M = {}
M.name = "go_binary"

local function strlist(v)
  if v == nil then return {} end
  if type(v) == "string" then return { v } end
  if type(v) == "table" then return v end
  error("go_binary: list param must be string or list")
end

function M.validate(p)
  if p.name == nil then error("go_binary: missing required param 'name'") end
  return true
end

function M.expand(p)
  M.validate(p)
  local pkg = p.package or "."
  local short = p.name:match("([^:]+)$"):gsub("/", "_")
  local out = p.out or (".ebuild/bin/" .. short)
  local argv = "go build -o " .. out
  local tags = strlist(p.tags)
  if #tags > 0 then argv = argv .. " -tags " .. table.concat(tags, ",") end
  if p.ldflags then argv = argv .. " -ldflags " .. p.ldflags end
  argv = argv .. " " .. pkg
  if p.cgo == nil or p.cgo == false then argv = "CGO_ENABLED=0 " .. argv end
  return { { rule = "go-build", target = out, workdir = ".", argv = argv } }
end

return M
