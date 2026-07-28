class Reminal < Formula
  desc "Remote terminal access — secure, zero-config alternative to SSH"
  homepage "https://github.com/harshalgajjar/Reminal"
  version "1.9.3"
  license "AGPL-3.0-or-later"

  head do
    url "https://github.com/harshalgajjar/Reminal.git", branch: "main"
  end

  on_macos do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.9.3/reminal_1.9.3_darwin_arm64.tar.gz"
      sha256 "02f30a0a7ee1017b07bae19e4c90fb5ed3f85fa412f031709e039bd6493d8a34"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.9.3/reminal_1.9.3_darwin_amd64.tar.gz"
      sha256 "cc4807f640893ca0bdbf48733d1dcadb1cd273a143a0643416f961d60405571a"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.9.3/reminal_1.9.3_linux_arm64.tar.gz"
      sha256 "a06320303651724cd31f1c3a017a84174e86672214e57fdb9c1de6366c127e22"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.9.3/reminal_1.9.3_linux_amd64.tar.gz"
      sha256 "3cd0143882ff045126129852cf5c2e44506f4d9d2f8e6c4f4737f033ce00cd5f"
    end
  end

  depends_on "go" => :build if build.head?

  def install
    if build.head?
      system "go", "build", "-ldflags=#{ldflags}", "-o", bin/"reminal", "./cmd/reminal"
    else
      bin.install "reminal"
    end
  end

  def ldflags
    "-s -w " \
      "-X main.version=#{version} " \
      "-X github.com/reminal/reminal/internal/config.DefaultCloudRelay=wss://reminal-relay.futuristic.workers.dev/ws " \
      "-X github.com/reminal/reminal/internal/config.DefaultCloudWeb=https://reminal-relay.futuristic.workers.dev"
  end

  def caveats
    <<~EOS
      reminal connects to the hosted relay automatically — no setup needed.

        reminal              # share your terminal
        reminal --connect ID --pin PIN
    EOS
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/reminal version")
  end
end
