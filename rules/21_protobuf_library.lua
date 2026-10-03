-- rules/21_protobuf_library.lua — generate protobuf sources per language.
-- params: name, srcs(list of .proto), languages(list), out_dir(opt).
local M = {}
M.name = "protobuf_library"

local function strlist(v)
  if v == nil then return {} end
  if type(v) == "string" then return { v } end
  if type(v) == "table" then return v end
  error("protobuf_library: list param must be string or list")
end

function M.validate(p)
  if p.name == nil then error("protobuf_library: missing required param 'name'") end
  if p.srcs == nil then error("protobuf_library: missing required param 'srcs'") end
  for _, s in ipairs(strlist(p.srcs)) do
    if not s:match("%.proto$") then error("protobuf_library: '" .. s .. "' not .proto") end
  end
  if p.languages == nil then error("protobuf_library: missing required param 'languages'") end
  return true
end

function M.expand(p)
  M.validate(p)
  local langs = { go = "--go_out", python = "--python_out", java = "--java_out",
                  cpp = "--cpp_out", ts = "--plugin=protoc-gen-ts" }
  local outdir = p.out_dir or ".ebuild/proto"
  local steps = {}
  for _, lang in ipairs(strlist(p.languages)) do
    local flag = langs[lang]
    if flag == nil then error("protobuf_library: unsupported language '" .. lang .. "'") end
    local short = p.name:match("([^:]+)$"):gsub("/", "_")
    local target = outdir .. "/" .. short .. "." .. lang .. ".gen"
    local argv = "protoc " .. flag .. "=" .. outdir .. " " ..
      table.concat(strlist(p.srcs), " ") .. " && touch " .. target
    steps[#steps + 1] = { rule = "gen", target = target, workdir = ".", argv = argv }
  end
  return steps
end

return M
