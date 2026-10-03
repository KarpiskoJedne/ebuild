-- rules/08_go_library.lua — vet + build a Go library package.
local M = {}
M.name = "go_library"

function M.validate(p)
  if p.name == nil then error("go_library: missing required param 'name'") end
  return true
end

function M.expand(p)
  M.validate(p)
  local pkg = p.package or "."
  local argv = "CGO_ENABLED=0 go vet " .. pkg .. " && CGO_ENABLED=0 go build " .. pkg
  local short = p.name:match("([^:]+)$"):gsub("/", "_")
  return { { rule = "go-build", target = ".ebuild/lib/" .. short .. ".a", workdir = ".", argv = argv } }
end

return M
