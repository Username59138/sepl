local total = 0
for i = 0, 10000000 - 1 do
    if i % 3 == 0 then total = total + i end
end
print(total)
