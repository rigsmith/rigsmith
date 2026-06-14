using Microsoft.VisualStudio.TestTools.UnitTesting;

namespace Acme.Mtp.Tests;

[TestClass]
public class WidgetTests
{
    [TestMethod]
    public void Builds() => Assert.AreEqual(1, 1);

    [TestMethod]
    [DataRow(3)]
    [DataRow(4)]
    public void Counts(int n) => Assert.IsTrue(n > 0);
}

[TestClass]
public class GadgetTests
{
    [TestMethod]
    public void Works() => Assert.IsTrue(true);
}

// Not a test class — must not be enumerated.
public class Helper
{
    public int Value => 42;
}
