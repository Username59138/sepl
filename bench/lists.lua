local items = {}
for i = 0, 1000000 - 1 do
    items[#items + 1] = i * 2
end
local total = 0
for _, x in ipairs(items) do total = total + x end
local words, n = {}, 0
for i = 0, 200000 - 1 do
    local k = tostring(i % 1000)
    if words[k] == nil then n = n + 1 end
    words[k] = (words[k] or 0) + 1
end
print(total, n)
