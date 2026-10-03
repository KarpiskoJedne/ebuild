-- rules/29_php_composer.lua — validate + install + dump-autoload via composer.
-- params: name, dir(opt, default "."), no_dev(bool,opt), out(opt marker).
local M = {}
M.name = "php_composer"

function M.validate(p)
  if p.name == nil then error("php_composer: missing required param 'name'") end
  return true
end

function M.expand(p)
  M.validate(p)
  local dir = p.dir or "."
  local argv = "composer validate --no-check-publish --working-dir=" .. dir ..
    " && composer install --no-interaction --prefer-dist --working-dir=" .. dir
  if p.no_dev ~= false then argv = argv .. " --no-dev --optimize-autoloader" end
  local short = p.name:match("([^:]+)$"):gsub("/", "_")
  local out = p.out or (".ebuild/php/" .. short .. ".ok")
  return { { rule = "php", target = out, workdir = ".", argv = argv .. " && touch " .. out } }
end

return M
