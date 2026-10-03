-- rules/22_grpc_service.lua — generate gRPC stubs + gateway from protos.
local M = {}
M.name = "grpc_service"

local function strlist(v)
  if v == nil then return {} end
  if type(v) == "string" then return { v } end
  if type(v) == "table" then return v end
  error("grpc_service: list param must be string or list")
end

function M.validate(p)
  if p.name == nil then error("grpc_service: missing required param 'name'") end
  if p.srcs == nil then error("grpc_service: missing required param 'srcs'") end
  if p.languages == nil then error("grpc_service: missing required param 'languages'") end
  return true
end

function M.expand(p)
  M.validate(p)
  local plugins = { go = "--go-grpc_out", python = "--grpc_python_out",
                    java = "--grpc-java_out", ts = "--plugin=protoc-gen-grpc-ts" }
  local outdir = p.out_dir or ".ebuild/grpc"
  local steps = {}
  for _, lang in ipairs(strlist(p.languages)) do
    local plugin = plugins[lang]
    if plugin == nil then error("grpc_service: unsupported language '" .. lang .. "'") end
    local short = p.name:match("([^:]+)$"):gsub("/", "_")
    local target = outdir .. "/" .. short .. "." .. lang .. ".grpc.gen"
    local argv = "protoc " .. plugin .. "=" .. outdir .. " " ..
      table.concat(strlist(p.srcs), " ") .. " && touch " .. target
    steps[#steps + 1] = { rule = "gen", target = target, workdir = ".", argv = argv }
  end
  return steps
end

return M
