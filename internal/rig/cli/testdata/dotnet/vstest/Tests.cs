using Xunit;

namespace Acme.Vstest.Tests;

public class CalculatorTests
{
    [Fact]
    public void Adds() => Assert.Equal(2, 1 + 1);

    [Theory]
    [InlineData(2, 2)]
    [InlineData(3, 3)]
    public void Echoes(int n, int expected) => Assert.Equal(expected, n);
}

public class StringTests
{
    [Fact]
    public void Concats() => Assert.Equal("ab", "a" + "b");
}

// Not a test class — must not be enumerated.
public class Helper
{
    public int Value => 42;
}
