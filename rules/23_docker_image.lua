-- rules/23_docker_image.lua — build (and optionally push) a container image.
-- params: name, dockerfile(opt), context(opt), tags(list,opt), push(bool,opt).
local M = {}
M.name = "docker_image"

local function strlist(v)
  if v == nil then return {} end
  if type(v) == "string" then return { v } end
  if type(v) == "table" then return v end
  error("docker_image: list param must be string or list")
end

function M.validate(p)
  if p.name == nil then error("docker_image: missing required param 'name'") end
  return true
end

function M.expand(p)
  M.validate(p)
  local tags = strlist(p.tags)
  if #tags == 0 then
    tags = { "ebuild/" .. p.name:match("([^:]+)$"):gsub("/", "-"):lower() .. ":latest" }
  end
  local tflags = {}
  for _, t in ipairs(tags) do tflags[#tflags + 1] = "-t " .. t end
  local argv = "docker build -f " .. (p.dockerfile or "Dockerfile") .. " " ..
    table.concat(tflags, " ") .. " " .. (p.context or ".")
  if p.push then argv = argv .. " && docker push " .. tags[1] end
  local short = p.name:match("([^:]+)$"):gsub("/", "_")
  return { { rule = "gen", target = ".ebuild/docker/" .. short .. ".iid",
             workdir = ".", argv = argv .. " --iidfile .ebuild/docker/" .. short .. ".iid" } }
end

return M
